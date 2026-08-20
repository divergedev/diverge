// Package proxy implements the Diverge reverse proxy for subdomain-based
// routing of requests to preview environments.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// ResponseHeaderEnvironment is the HTTP response header that identifies which
// preview environment served the request.
const ResponseHeaderEnvironment = "X-Diverge-Environment"

// ErrEnvironmentNotFound is returned when an environment cannot be found.
var ErrEnvironmentNotFound = errors.New("environment not found")

// Config holds proxy configuration
type Config struct {
	// BaseURL is the upstream URL (e.g., https://app.staging.example.com)
	BaseURL string
	// HeaderKey is the header to inject (default: x-diverge-env)
	HeaderKey string
	// PreviewDomain is the wildcard domain (e.g., preview.example.com)
	PreviewDomain string
	// Port to listen on
	Port int
	// HideEnvironmentList hides active environments on 404
	HideEnvironmentList bool
}

// Server is the Magic URL reverse proxy server
type Server struct {
	config    Config
	envLister EnvironmentLister
	proxy     *httputil.ReverseProxy
	readiness ReadinessChecker
}

// EnvironmentLister resolves environment names from the K8s API
type EnvironmentLister interface {
	GetEnvironment(ctx context.Context, name string) (*EnvironmentInfo, error)
	ListEnvironments(ctx context.Context) ([]EnvironmentInfo, error)
}

// ReadinessChecker is optionally implemented by an EnvironmentLister to
// indicate whether its internal cache has synced with the Kubernetes API.
type ReadinessChecker interface {
	HasSynced() bool
}

// EnvironmentInfo holds metadata about a resolved preview environment,
// including its deployment phase and upstream target URL.
type EnvironmentInfo struct {
	Name      string
	Phase     string
	URL       string
	BaseURL   string
	HeaderKey string
}

// NewServer creates a new reverse proxy Server. It parses the BaseURL from cfg,
// configures an httputil.ReverseProxy director, and defaults the header key to
// "x-diverge-env" if not set. If lister implements ReadinessChecker, readiness
// probes will reflect cache sync state.
func NewServer(cfg Config, lister EnvironmentLister) (*Server, error) {
	targetURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	s := &Server{
		config:    cfg,
		envLister: lister,
		proxy: &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				r.SetURL(targetURL)
				r.Out.Host = targetURL.Host
			},
		},
	}
	if s.config.HeaderKey == "" {
		s.config.HeaderKey = "x-diverge-env"
	}
	if r, ok := lister.(ReadinessChecker); ok {
		s.readiness = r
	}
	return s, nil
}

// ServeHTTP handles incoming HTTP requests. It serves health and readiness
// probes, validates the host against the preview domain, resolves the target
// environment, and either renders a status page or proxies to the upstream.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Health checks bypass domain validation
	if r.URL.Path == "/-/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	if r.URL.Path == "/-/readyz" {
		if s.readiness != nil && !s.readiness.HasSynced() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("cache not synced"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	host := r.Host
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}

	if !strings.HasSuffix(host, s.config.PreviewDomain) {
		http.Error(w, "Invalid domain", http.StatusBadRequest)
		return
	}

	envName := extractEnvName(host, s.config.PreviewDomain)
	if envName == "" {
		http.Error(w, "Environment name not found in domain", http.StatusBadRequest)
		return
	}

	envInfo, err := s.envLister.GetEnvironment(r.Context(), envName)
	if err != nil {
		if errors.Is(err, ErrEnvironmentNotFound) {
			envs, _ := s.envLister.ListEnvironments(r.Context())
			renderNotFound(w, envName, envs, s.config.HideEnvironmentList)
			return
		}
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	switch envInfo.Phase {
	case string(v1alpha1.PhaseDeploying), string(v1alpha1.PhasePending):
		renderLoading(w, envInfo)
		return
	case string(v1alpha1.PhaseFailed):
		renderError(w, envInfo, "Deployment failed")
		return
	case string(v1alpha1.PhaseRunning):
		r.Header.Set(s.config.HeaderKey, envInfo.Name)
		w.Header().Set(ResponseHeaderEnvironment, envInfo.Name)
		s.proxy.ServeHTTP(w, r)
		return
	default:
		renderError(w, envInfo, "Unknown phase: "+envInfo.Phase)
		return
	}
}

func extractEnvName(host, previewDomain string) string {
	envName := strings.TrimSuffix(host, "."+previewDomain)
	if envName == host || envName == "" {
		return ""
	}
	return envName
}
