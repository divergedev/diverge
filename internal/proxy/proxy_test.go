package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockEnvironmentLister struct {
	envs map[string]*EnvironmentInfo
	err  error
}

func (m *mockEnvironmentLister) GetEnvironment(ctx context.Context, name string) (*EnvironmentInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	if env, ok := m.envs[name]; ok {
		return env, nil
	}
	return nil, fmt.Errorf("environment not found")
}

func (m *mockEnvironmentLister) ListEnvironments(ctx context.Context) ([]EnvironmentInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	var list []EnvironmentInfo
	for _, env := range m.envs {
		list = append(list, *env)
	}
	return list, nil
}

func setupTestServer(t *testing.T, lister *mockEnvironmentLister) (*Server, *httptest.Server) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envHeader := r.Header.Get("x-diverge-env")
		w.Header().Set("X-Received-Env", envHeader)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Proxied: " + envHeader))
	}))
	t.Cleanup(backend.Close)

	cfg := Config{
		BaseURL:       backend.URL,
		HeaderKey:     "x-diverge-env",
		PreviewDomain: "preview.example.com",
		Port:          8080,
	}

	server, err := NewServer(cfg, lister)
	require.NoError(t, err)

	return server, backend
}

func TestProxyRunningEnvironment(t *testing.T) {
	lister := &mockEnvironmentLister{
		envs: map[string]*EnvironmentInfo{
			"mr-42": {
				Name:  "mr-42",
				Phase: string(v1alpha1.PhaseRunning),
			},
		},
	}
	server, _ := setupTestServer(t, lister)

	req := httptest.NewRequest(http.MethodGet, "http://mr-42.preview.example.com/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Proxied: mr-42", w.Body.String())
	assert.Equal(t, "mr-42", w.Header().Get("X-Received-Env"))
	assert.Equal(t, "mr-42", w.Header().Get(ResponseHeaderEnvironment))
}

func TestProxyDeployingEnvironment(t *testing.T) {
	lister := &mockEnvironmentLister{
		envs: map[string]*EnvironmentInfo{
			"mr-43": {
				Name:  "mr-43",
				Phase: string(v1alpha1.PhaseDeploying),
			},
		},
	}
	server, _ := setupTestServer(t, lister)

	req := httptest.NewRequest(http.MethodGet, "http://mr-43.preview.example.com/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code) // renderLoading writes 503
	assert.Contains(t, w.Body.String(), "mr-43")
	assert.Nil(t, w.Header().Values(ResponseHeaderEnvironment))
}

func TestProxyNotFoundEnvironment(t *testing.T) {
	lister := &mockEnvironmentLister{
		envs: map[string]*EnvironmentInfo{},
	}
	server, _ := setupTestServer(t, lister)

	req := httptest.NewRequest(http.MethodGet, "http://mr-44.preview.example.com/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "mr-44")
	assert.Nil(t, w.Header().Values(ResponseHeaderEnvironment))
}

func TestProxyFailedEnvironment(t *testing.T) {
	lister := &mockEnvironmentLister{
		envs: map[string]*EnvironmentInfo{
			"mr-45": {
				Name:  "mr-45",
				Phase: string(v1alpha1.PhaseFailed),
			},
		},
	}
	server, _ := setupTestServer(t, lister)

	req := httptest.NewRequest(http.MethodGet, "http://mr-45.preview.example.com/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "mr-45")
	assert.Contains(t, w.Body.String(), "Deployment failed")
	assert.Nil(t, w.Header().Values(ResponseHeaderEnvironment))
}

func TestProxyExtractEnvName(t *testing.T) {
	server, _ := setupTestServer(t, &mockEnvironmentLister{})

	tests := []struct {
		host          string
		expectedCode  int
		expectedError string
	}{
		{"mr-42.preview.example.com", http.StatusNotFound, ""},
		{"mr-42.preview.example.com:8080", http.StatusNotFound, ""},
		{"invalid.example.com", http.StatusBadRequest, "Invalid domain\n"},
		{"preview.example.com", http.StatusBadRequest, "Environment name not found in domain\n"},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tt.host+"/", nil)
			w := httptest.NewRecorder()

			server.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedCode, w.Code)
			if tt.expectedError != "" {
				assert.Equal(t, tt.expectedError, w.Body.String())
			}
		})
	}
}

func TestProxy503OnCacheError(t *testing.T) {
	lister := &mockEnvironmentLister{
		err: errors.New("cache sync failed"),
	}
	server, _ := setupTestServer(t, lister)

	req := httptest.NewRequest(http.MethodGet, "http://mr-42.preview.example.com/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "Service Unavailable")
}

func TestProxyListEnvironments(t *testing.T) {
	lister := &mockEnvironmentLister{
		envs: map[string]*EnvironmentInfo{
			"mr-42": {Name: "mr-42", Phase: string(v1alpha1.PhaseRunning)},
		},
	}
	envs, err := lister.ListEnvironments(context.Background())
	assert.NoError(t, err)
	assert.Len(t, envs, 1)
	assert.Equal(t, "mr-42", envs[0].Name)
}

func TestHealthEndpoint(t *testing.T) {
	server := &Server{
		config: Config{PreviewDomain: "preview.example.com"},
	}
	req := httptest.NewRequest("GET", "http://localhost/-/healthz", nil)
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "ok", rr.Body.String())
}

type mockReadiness struct {
	synced bool
}

func (m *mockReadiness) HasSynced() bool {
	return m.synced
}

func TestReadyEndpointSynced(t *testing.T) {
	server := &Server{
		config:    Config{PreviewDomain: "preview.example.com"},
		readiness: &mockReadiness{synced: true},
	}
	req := httptest.NewRequest("GET", "http://localhost/-/readyz", nil)
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestReadyEndpointNotSynced(t *testing.T) {
	server := &Server{
		config:    Config{PreviewDomain: "preview.example.com"},
		readiness: &mockReadiness{synced: false},
	}
	req := httptest.NewRequest("GET", "http://localhost/-/readyz", nil)
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

func TestReadyEndpointNoChecker(t *testing.T) {
	server := &Server{
		config: Config{PreviewDomain: "preview.example.com"},
	}
	req := httptest.NewRequest("GET", "http://localhost/-/readyz", nil)
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}
