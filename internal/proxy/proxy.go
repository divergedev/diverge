package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// Config holds proxy configuration
type Config struct {
	// BaseURL is the upstream URL (e.g., https://app.staging.example.com)
	BaseURL       string
	// HeaderKey is the header to inject (default: x-diverge-env)
	HeaderKey     string
	// PreviewDomain is the wildcard domain (e.g., preview.example.com)
	PreviewDomain string
	// Port to listen on
	Port          int
}

// Server is the Magic URL reverse proxy server
type Server struct {
	config    Config
	envLister EnvironmentLister
	proxy     *httputil.ReverseProxy
}

// EnvironmentLister resolves environment names from the K8s API
type EnvironmentLister interface {
	GetEnvironment(ctx context.Context, name string) (*EnvironmentInfo, error)
	ListEnvironments(ctx context.Context) ([]EnvironmentInfo, error)
}

type EnvironmentInfo struct {
	Name      string
	Phase     string
	URL       string
	BaseURL   string
	HeaderKey string
}

func NewServer(cfg Config, lister EnvironmentLister) (*Server, error) {
	targetURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	return &Server{
		config:    cfg,
		envLister: lister,
		proxy: &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = targetURL.Scheme
				req.URL.Host = targetURL.Host
				req.Host = targetURL.Host
			},
		},
	}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}

	if !strings.HasSuffix(host, s.config.PreviewDomain) {
		http.Error(w, "Invalid domain", http.StatusBadRequest)
		return
	}

	envName := strings.TrimSuffix(host, "."+s.config.PreviewDomain)
	if envName == host || envName == "" {
		http.Error(w, "Environment name not found in domain", http.StatusBadRequest)
		return
	}

	envInfo, err := s.envLister.GetEnvironment(r.Context(), envName)
	if err != nil {
		if err == ErrCacheNotSynced {
			http.Error(w, "Service Unavailable: Cache syncing", http.StatusServiceUnavailable)
			return
		}
		envs, _ := s.envLister.ListEnvironments(r.Context())
		renderNotFound(w, envName, envs)
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
		s.proxy.ServeHTTP(w, r)
		return
	default:
		renderError(w, envInfo, "Unknown phase: "+envInfo.Phase)
		return
	}
}
