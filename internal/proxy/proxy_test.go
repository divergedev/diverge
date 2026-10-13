package proxy

import (
	"context"
	"errors"
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
	return nil, ErrEnvironmentNotFound
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

func TestProxyMigratingEnvironment(t *testing.T) {
	lister := &mockEnvironmentLister{
		envs: map[string]*EnvironmentInfo{
			"mr-migrate": {
				Name:  "mr-migrate",
				Phase: string(v1alpha1.PhaseMigrating),
			},
		},
	}
	server, _ := setupTestServer(t, lister)

	req := httptest.NewRequest(http.MethodGet, "http://mr-migrate.preview.example.com/", nil)
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "mr-migrate")
	assert.Nil(t, w.Header().Values(ResponseHeaderEnvironment))
}

func TestProxyConnectionHeaderStrippingResistance(t *testing.T) {
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
	req.Header.Set("Connection", "x-diverge-env, keep-alive")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Proxied: mr-42", w.Body.String())
	assert.Equal(t, "mr-42", w.Header().Get("X-Received-Env"))
}

func TestProxySetPreviewCookieViaQueryParam(t *testing.T) {
	lister := &mockEnvironmentLister{
		envs: map[string]*EnvironmentInfo{
			"pr-42": {
				Name:  "pr-42",
				Phase: string(v1alpha1.PhaseRunning),
			},
		},
	}
	server, _ := setupTestServer(t, lister)

	// 1. GET /?_diverge_env=pr-42 sets Set-Cookie: diverge_env=pr-42 and redirects (302) to /
	req := httptest.NewRequest(http.MethodGet, "http://preview.example.com/?_diverge_env=pr-42", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/", w.Header().Get("Location"))
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, PreviewCookieName, cookies[0].Name)
	assert.Equal(t, "pr-42", cookies[0].Value)
	assert.Equal(t, "/", cookies[0].Path)
	assert.Equal(t, 86400, cookies[0].MaxAge)
	assert.True(t, cookies[0].HttpOnly)

	// 2. Subsequent GET /api/data on Host: preview.example.com with Cookie: diverge_env=pr-42 routes to pr-42
	req2 := httptest.NewRequest(http.MethodGet, "http://preview.example.com/api/data", nil)
	req2.AddCookie(&http.Cookie{Name: PreviewCookieName, Value: "pr-42"})
	w2 := httptest.NewRecorder()
	server.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "Proxied: pr-42", w2.Body.String())
	assert.Equal(t, "pr-42", w2.Header().Get("X-Received-Env"))
	assert.Equal(t, "pr-42", w2.Header().Get(ResponseHeaderEnvironment))

	// 3. GET /?_diverge_env=clear expires the cookie
	req3 := httptest.NewRequest(http.MethodGet, "http://preview.example.com/?_diverge_env=clear", nil)
	w3 := httptest.NewRecorder()
	server.ServeHTTP(w3, req3)

	assert.Equal(t, http.StatusFound, w3.Code)
	assert.Equal(t, "/", w3.Header().Get("Location"))
	clearCookies := w3.Result().Cookies()
	require.Len(t, clearCookies, 1)
	assert.Equal(t, PreviewCookieName, clearCookies[0].Name)
	assert.Equal(t, "", clearCookies[0].Value)
	assert.Equal(t, -1, clearCookies[0].MaxAge)

	// 4. GET /-/select?env=pr-42 also sets cookie and redirects to /
	req4 := httptest.NewRequest(http.MethodGet, "https://preview.example.com/-/select?env=pr-42", nil)
	w4 := httptest.NewRecorder()
	server.ServeHTTP(w4, req4)

	assert.Equal(t, http.StatusFound, w4.Code)
	assert.Equal(t, "/", w4.Header().Get("Location"))
	selectCookies := w4.Result().Cookies()
	require.Len(t, selectCookies, 1)
	assert.Equal(t, "pr-42", selectCookies[0].Value)
	assert.True(t, selectCookies[0].Secure)

	// 5. GET /?_diverge_env=reset also expires the cookie
	req5 := httptest.NewRequest(http.MethodGet, "http://preview.example.com/?_diverge_env=reset", nil)
	w5 := httptest.NewRecorder()
	server.ServeHTTP(w5, req5)

	assert.Equal(t, http.StatusFound, w5.Code)
	assert.Equal(t, "/", w5.Header().Get("Location"))
	resetCookies := w5.Result().Cookies()
	require.Len(t, resetCookies, 1)
	assert.Equal(t, PreviewCookieName, resetCookies[0].Name)
	assert.Equal(t, "", resetCookies[0].Value)
	assert.Equal(t, -1, resetCookies[0].MaxAge)

	// 6. GET /-/select?reset=1 also expires the cookie and strips reset param
	req6 := httptest.NewRequest(http.MethodGet, "http://preview.example.com/-/select?reset=1", nil)
	w6 := httptest.NewRecorder()
	server.ServeHTTP(w6, req6)

	assert.Equal(t, http.StatusFound, w6.Code)
	assert.Equal(t, "/", w6.Header().Get("Location"))
	require.Len(t, w6.Result().Cookies(), 1)
	assert.Equal(t, -1, w6.Result().Cookies()[0].MaxAge)
}

func TestProxyInvalidPreviewEnvAndSuffixSpoofing(t *testing.T) {
	server, _ := setupTestServer(t, &mockEnvironmentLister{
		envs: map[string]*EnvironmentInfo{
			"pr-42": {Name: "pr-42", Phase: string(v1alpha1.PhaseRunning)},
		},
	})

	// Invalid _diverge_env (e.g. ../evil) returns 400 Bad Request
	req := httptest.NewRequest(http.MethodGet, "http://preview.example.com/?_diverge_env=../evil", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid preview environment name")

	// Suffix spoofing (Host: evil-preview.example.com) returns 400 Bad Request
	reqSpoof := httptest.NewRequest(http.MethodGet, "http://evil-preview.example.com/", nil)
	wSpoof := httptest.NewRecorder()
	server.ServeHTTP(wSpoof, reqSpoof)
	assert.Equal(t, http.StatusBadRequest, wSpoof.Code)
	assert.Equal(t, "Invalid domain\n", wSpoof.Body.String())

	// Header fallback on apex domain
	reqHdr := httptest.NewRequest(http.MethodGet, "http://preview.example.com/", nil)
	reqHdr.Header.Set("x-diverge-env", "pr-42")
	wHdr := httptest.NewRecorder()
	server.ServeHTTP(wHdr, reqHdr)
	assert.Equal(t, http.StatusOK, wHdr.Code)
	assert.Equal(t, "pr-42", wHdr.Header().Get(ResponseHeaderEnvironment))
}

func TestProxySubdomainSelectionRedirectsToApexAndPreservesEscapedPath(t *testing.T) {
	lister := &mockEnvironmentLister{
		envs: map[string]*EnvironmentInfo{
			"pr-42": {
				Name:  "pr-42",
				Phase: string(v1alpha1.PhaseRunning),
			},
		},
	}
	server, _ := setupTestServer(t, lister)

	// 1. Selecting an env from a subdomain redirects to the apex endpoint first before setting the cookie
	reqSub := httptest.NewRequest(http.MethodGet, "http://old-env.preview.example.com:8080/documents/a%3Fb?_diverge_env=pr-42&foo=1", nil)
	wSub := httptest.NewRecorder()
	server.ServeHTTP(wSub, reqSub)

	assert.Equal(t, http.StatusFound, wSub.Code)
	assert.Empty(t, wSub.Result().Cookies(), "subdomain redirect should not set host-only cookie on subdomain")
	assert.Equal(t, "http://preview.example.com:8080/documents/a%3Fb?_diverge_env=pr-42&foo=1", wSub.Header().Get("Location"))

	// 2. Following the redirect on the apex host sets the cookie and preserves %3F in the destination path
	reqApex := httptest.NewRequest(http.MethodGet, wSub.Header().Get("Location"), nil)
	wApex := httptest.NewRecorder()
	server.ServeHTTP(wApex, reqApex)

	assert.Equal(t, http.StatusFound, wApex.Code)
	assert.Equal(t, "/documents/a%3Fb?foo=1", wApex.Header().Get("Location"))
	require.Len(t, wApex.Result().Cookies(), 1)
	assert.Equal(t, "pr-42", wApex.Result().Cookies()[0].Value)

	// 3. Clearing from a subdomain also redirects to the apex endpoint first
	reqClearSub := httptest.NewRequest(http.MethodGet, "https://old-env.preview.example.com/?_diverge_env=clear", nil)
	wClearSub := httptest.NewRecorder()
	server.ServeHTTP(wClearSub, reqClearSub)

	assert.Equal(t, http.StatusFound, wClearSub.Code)
	assert.Empty(t, wClearSub.Result().Cookies())
	assert.Equal(t, "https://preview.example.com/?_diverge_env=clear", wClearSub.Header().Get("Location"))

	// 4. 404 page on a missing subdomain renders the Switch via Cookie link against the apex host
	req404 := httptest.NewRequest(http.MethodGet, "http://missing.preview.example.com:8080/", nil)
	w404 := httptest.NewRecorder()
	server.ServeHTTP(w404, req404)

	assert.Equal(t, http.StatusNotFound, w404.Code)
	assert.Contains(t, w404.Body.String(), `href="//preview.example.com:8080/?_diverge_env=pr-42"`)
}
