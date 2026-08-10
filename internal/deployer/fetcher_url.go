package deployer

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// maxManifestSize is the maximum allowed response body size for manifest
// fetching (10 MB). Prevents unbounded reads from exhausting controller memory.
const maxManifestSize = 10 * 1024 * 1024

// URLFetcher downloads pre-rendered YAML manifests from an HTTP endpoint.
// The URL is read from env.Spec.Deploy.Manifests.URL.
// An optional auth token can be set via the AuthToken field.
type URLFetcher struct {
	HTTPClient *http.Client
	AuthToken  string
	// SkipURLValidation disables SSRF protection (HTTPS enforcement,
	// private IP blocking). Only set to true for development/testing.
	SkipURLValidation bool
}

func (f *URLFetcher) Fetch(ctx context.Context, env *v1alpha1.Environment) ([]unstructured.Unstructured, error) {
	if env.Spec.Deploy.Manifests == nil || env.Spec.Deploy.Manifests.URL == "" {
		return nil, fmt.Errorf("manifest URL not specified in environment %s/%s", env.Namespace, env.Name)
	}

	manifestURL := env.Spec.Deploy.Manifests.URL

	// CR1: Validate URL before sending any credentials
	if !f.SkipURLValidation {
		if err := validateManifestURL(manifestURL); err != nil {
			return nil, fmt.Errorf("invalid manifest URL %q: %w", manifestURL, err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if f.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+f.AuthToken)
	}

	// Use a per-request client copy that validates redirect targets
	// to prevent SSRF via open redirects (e.g., HTTPS → HTTP loopback).
	httpClient := f.HTTPClient
	if !f.SkipURLValidation {
		clientCopy := *f.HTTPClient
		clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			if err := validateManifestURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect to %q blocked: %w", req.URL.String(), err)
			}
			return nil
		}
		httpClient = &clientCopy
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch manifests from %s: %w", manifestURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching manifests from %s", resp.StatusCode, manifestURL)
	}

	// CR5: Limit response body size to prevent OOM from unbounded reads
	limitedReader := io.LimitReader(resp.Body, maxManifestSize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if int64(len(body)) > maxManifestSize {
		return nil, fmt.Errorf("manifest response exceeds maximum size of %d bytes", maxManifestSize)
	}

	return parseMultiDocYAML(body)
}

// validateManifestURL ensures the URL is safe to fetch from:
// - Must be HTTPS (unless localhost for dev)
// - Must not target private, loopback, or link-local addresses
func validateManifestURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("failed to parse URL: %w", err)
	}

	// Require HTTPS
	if parsed.Scheme != "https" {
		return fmt.Errorf("scheme must be https, got %q", parsed.Scheme)
	}

	// Resolve hostname to check for private/loopback IPs
	hostname := parsed.Hostname()
	ips, err := net.LookupHost(hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve host %q: %w", hostname, err)
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() {
			return fmt.Errorf("loopback addresses are not allowed: %s", ipStr)
		}
		if ip.IsPrivate() {
			return fmt.Errorf("private network addresses are not allowed: %s", ipStr)
		}
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("link-local addresses are not allowed: %s", ipStr)
		}
		if strings.HasPrefix(ipStr, "169.254.") {
			return fmt.Errorf("metadata endpoint addresses are not allowed: %s", ipStr)
		}
	}

	return nil
}
