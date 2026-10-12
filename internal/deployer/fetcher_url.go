package deployer

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

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

// NewURLFetcher creates a URLFetcher with a DNS-pinning HTTP transport that
// validates resolved IPs at dial time to prevent SSRF and DNS rebinding (TOCTOU).
func NewURLFetcher(authToken string) *URLFetcher {
	return &URLFetcher{
		HTTPClient: newSafeHTTPClient(),
		AuthToken:  authToken,
	}
}

func newSafeHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid dial address %q: %w", addr, err)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve host %q: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no IP addresses found for host %q", host)
			}
			for _, ipAddr := range ips {
				if err := isDisallowedIP(ipAddr.IP); err != nil {
					return nil, fmt.Errorf("blocked connection to %s (%s): %w", host, ipAddr.IP.String(), err)
				}
			}
			// Pin to the first validated IP to prevent DNS rebinding TOCTOU
			pinnedAddr := net.JoinHostPort(ips[0].IP.String(), port)
			return dialer.DialContext(ctx, network, pinnedAddr)
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}
}

// Fetch performs its designated operation.
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
	baseClient := f.HTTPClient
	if baseClient == nil {
		if f.SkipURLValidation {
			baseClient = http.DefaultClient
		} else {
			baseClient = newSafeHTTPClient()
		}
	}

	httpClient := baseClient
	if !f.SkipURLValidation {
		clientCopy := *baseClient
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

// isDisallowedIP checks whether an IP address is in a reserved, private,
// loopback, link-local, unspecified, multicast, or cloud metadata range.
func isDisallowedIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("invalid nil IP address")
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	ipStr := ip.String()
	if strings.HasPrefix(ipStr, "169.254.") {
		return fmt.Errorf("metadata endpoint addresses are not allowed: %s", ipStr)
	}
	if ip.IsLoopback() {
		return fmt.Errorf("loopback addresses are not allowed: %s", ipStr)
	}
	if ip.IsPrivate() {
		return fmt.Errorf("private network addresses are not allowed: %s", ipStr)
	}
	if ip.IsLinkLocalUnicast() {
		return fmt.Errorf("link-local addresses are not allowed: %s", ipStr)
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("unspecified addresses are not allowed: %s", ipStr)
	}
	if ip.IsMulticast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("multicast addresses are not allowed: %s", ipStr)
	}
	return nil
}

// validateManifestURL ensures the URL is safe to fetch from:
// - Must be HTTPS
// - Must not target private, loopback, link-local, unspecified, or multicast addresses
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
	if hostname == "" {
		return fmt.Errorf("URL hostname must not be empty")
	}
	if ip := net.ParseIP(hostname); ip != nil {
		return isDisallowedIP(ip)
	}

	ips, err := net.LookupHost(hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve host %q: %w", hostname, err)
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if err := isDisallowedIP(ip); err != nil {
			return err
		}
	}

	return nil
}
