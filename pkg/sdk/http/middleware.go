package http

import (
	"context"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/divergedev/diverge/pkg/sdk"
)

// DefaultHeaderKey is used by the middleware to extract the X-Diverge-Env header and inject it into the request context.
const DefaultHeaderKey = sdk.DefaultHeaderKey

// dns1123LabelRegex validates DNS-1123 label names (used for K8s names/namespaces).
var dns1123LabelRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]{0,61}[a-z0-9])?$`)

// Option configures the middleware behavior.
type Option func(*middlewareConfig)

type middlewareConfig struct {
	headerKey   string
	baseDomain  string
	queryParams []string
	stripQuery  bool
}

// WithBaseDomain configures subdomain-based environment extraction.
// For example, WithBaseDomain("preview.example.com") extracts "pr-123"
// from requests to "pr-123.preview.example.com".
func WithBaseDomain(domain string) Option {
	return func(c *middlewareConfig) {
		c.baseDomain = strings.ToLower(domain)
	}
}

// WithQueryParams configures query parameter keys to extract the environment from.
// For example, WithQueryParams("x-diverge-env", "diverge_env") checks both keys.
func WithQueryParams(keys ...string) Option {
	return func(c *middlewareConfig) {
		c.queryParams = keys
	}
}

// WithStripQueryParam removes the matched query parameter from the URL
// after extraction. The request is cloned before mutation to prevent data races.
//
// WARNING: Do not use with webhook handlers that verify HMAC signatures
// against the full URL — stripping parameters invalidates the signature.
func WithStripQueryParam(strip bool) Option {
	return func(c *middlewareConfig) {
		c.stripQuery = strip
	}
}

// PropagateEnvironment returns middleware that propagates the x-diverge-env
// header from incoming requests to outgoing requests via the request context.
//
// Example:
//
//	// Incoming handler:
//	mux.Use(divergehttp.PropagateEnvironment)
//
//	// Outgoing client:
//	client := &http.Client{
//	    Transport: divergehttp.RoundTripper(http.DefaultTransport),
//	}
func PropagateEnvironment(next http.Handler) http.Handler {
	return PropagateEnvironmentWithOptions()(next)
}

// PropagateEnvironmentWithOptions returns middleware with configurable extraction
// sources for the preview environment identifier.
//
// Precedence: Header → Query Parameter → Subdomain (first match wins).
//
// Example:
//
//	mux.Use(divergehttp.PropagateEnvironmentWithOptions(
//	    divergehttp.WithBaseDomain("preview.example.com"),
//	    divergehttp.WithQueryParams("x-diverge-env", "diverge_env"),
//	))
func PropagateEnvironmentWithOptions(opts ...Option) func(http.Handler) http.Handler {
	cfg := &middlewareConfig{
		headerKey: sdk.GetHeaderKey(),
	}
	// Support DIVERGE_BASE_DOMAIN env var as default
	if domain := os.Getenv("DIVERGE_BASE_DOMAIN"); domain != "" && cfg.baseDomain == "" {
		cfg.baseDomain = strings.ToLower(domain)
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			env := extractEnv(r, cfg)
			if env != "" {
				// Promote to header for downstream handlers
				r.Header.Set(cfg.headerKey, env)
				r = r.WithContext(sdk.WithEnvironment(r.Context(), env))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// extractEnv resolves the environment name from the request.
// Precedence: Header → Query Param → Subdomain.
func extractEnv(r *http.Request, cfg *middlewareConfig) string {
	// 1. Check existing header (highest priority)
	if env := r.Header.Get(cfg.headerKey); env != "" {
		if isValidEnvName(env) {
			return env
		}
		return ""
	}

	// 2. Check query parameters
	for _, key := range cfg.queryParams {
		if env := r.URL.Query().Get(key); env != "" {
			if !isValidEnvName(env) {
				continue
			}
			if cfg.stripQuery {
				// Clone request before mutating URL to prevent data races
				r2 := r.Clone(r.Context())
				q := r2.URL.Query()
				q.Del(key)
				r2.URL.RawQuery = q.Encode()
				*r = *r2
			}
			return env
		}
	}

	// 3. Check subdomain
	if cfg.baseDomain != "" {
		if env := extractSubdomain(r.Host, cfg.baseDomain); env != "" {
			return env
		}
	}

	return ""
}

// extractSubdomain extracts an environment name from the Host header.
// For "pr-123.preview.example.com:8080" with baseDomain "preview.example.com",
// returns "pr-123".
func extractSubdomain(host, baseDomain string) string {
	// Strip port
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host // No port present
	}
	h = strings.ToLower(h)

	// Strict dot-boundary suffix match
	suffix := "." + baseDomain
	if !strings.HasSuffix(h, suffix) {
		return ""
	}

	envName := strings.TrimSuffix(h, suffix)
	if envName == "" {
		return ""
	}

	// Validate RFC 1123 DNS label
	if !isValidEnvName(envName) {
		return ""
	}

	return envName
}

// isValidEnvName checks that a value is a valid Kubernetes DNS-1123 label.
// This prevents CRLF header injection, oversized values, and invalid
// identifiers from reaching CRD lookups.
func isValidEnvName(name string) bool {
	return dns1123LabelRegex.MatchString(name)
}

// RoundTripper wraps an http.RoundTripper to inject x-diverge-env from context
// into outgoing HTTP requests.
func RoundTripper(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &roundTripper{base: base}
}

type roundTripper struct {
	base http.RoundTripper
}

// RoundTrip injects the environment context into outgoing requests.
func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	env := sdk.EnvironmentFromContext(req.Context())
	if env != "" {
		// Clone request to avoid modifying original
		req = req.Clone(req.Context())
		req.Header.Set(DefaultHeaderKey, env)
	}
	return rt.base.RoundTrip(req)
}

// FromContext extracts the environment name from the context.
func FromContext(ctx context.Context) string {
	return sdk.EnvironmentFromContext(ctx)
}
