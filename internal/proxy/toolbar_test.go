package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolbarScriptEndpoint(t *testing.T) {
	lister := &mockEnvironmentLister{}
	server, _ := setupTestServer(t, lister)

	req := httptest.NewRequest(http.MethodGet, "http://preview.example.com/-/toolbar.js", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/javascript")
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))

	body := w.Body.String()
	assert.Contains(t, body, "customElements.define('diverge-toolbar'")
	assert.Contains(t, body, "attachShadow({ mode: 'open' })")
	assert.Contains(t, body, "🟣 Diverge:")
	assert.Contains(t, body, "Copy Magic Link (?_diverge_env=...)")
	assert.Contains(t, body, "Reset to Baseline (?_diverge_env=clear)")
	assert.Contains(t, body, "/-/api/context")
}

func TestToolbarContextEndpoint(t *testing.T) {
	lister := &mockEnvironmentLister{
		envs: map[string]*EnvironmentInfo{
			"pr-42": {Name: "pr-42", Phase: string(v1alpha1.PhaseRunning)},
			"pr-99": {Name: "pr-99", Phase: string(v1alpha1.PhaseDeploying)},
		},
	}
	server, _ := setupTestServer(t, lister)

	t.Run("resolves activeEnvironment from subdomain and lists environments", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://pr-42.preview.example.com/-/api/context", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

		var ctxResp ToolbarContextResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ctxResp))
		assert.Equal(t, "pr-42", ctxResp.ActiveEnvironment)
		assert.Equal(t, "x-diverge-env", ctxResp.HeaderKey)
		assert.Equal(t, "preview.example.com", ctxResp.PreviewDomain)
		require.Len(t, ctxResp.Environments, 2)
		assert.Equal(t, "pr-42", ctxResp.Environments[0].Name)
		assert.Equal(t, "pr-99", ctxResp.Environments[1].Name)
	})

	t.Run("resolves activeEnvironment from cookie on apex domain", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://preview.example.com/-/api/context", nil)
		req.AddCookie(&http.Cookie{Name: PreviewCookieName, Value: "pr-99"})
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var ctxResp ToolbarContextResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ctxResp))
		assert.Equal(t, "pr-99", ctxResp.ActiveEnvironment)
	})

	t.Run("omits environments when HideEnvironmentList is true", func(t *testing.T) {
		cfg := server.config
		cfg.HideEnvironmentList = true
		hiddenServer, err := NewServer(cfg, lister)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "http://pr-42.preview.example.com/-/api/context", nil)
		w := httptest.NewRecorder()
		hiddenServer.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var ctxResp ToolbarContextResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ctxResp))
		assert.Equal(t, "pr-42", ctxResp.ActiveEnvironment)
		assert.Empty(t, ctxResp.Environments)
	})
}

func TestToolbarHTMLInjectionAndOptOut(t *testing.T) {
	htmlDoc := "<!DOCTYPE html><html><head><title>App</title></head><body><h1>Hello Preview</h1></body></html>"

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		if r.URL.Path == "/compressed" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(htmlDoc))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(htmlDoc))
	}))
	t.Cleanup(backend.Close)

	lister := &mockEnvironmentLister{
		envs: map[string]*EnvironmentInfo{
			"pr-42": {Name: "pr-42", Phase: string(v1alpha1.PhaseRunning)},
		},
	}

	server, err := NewServer(Config{
		BaseURL:       backend.URL,
		HeaderKey:     "x-diverge-env",
		PreviewDomain: "preview.example.com",
	}, lister)
	require.NoError(t, err)

	t.Run("injects toolbar script before closing body tag and updates Content-Length", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://pr-42.preview.example.com/", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, `<script src="/-/toolbar.js" defer></script></body>`)
		assert.Equal(t, strconv.Itoa(len(body)), w.Header().Get("Content-Length"))
	})

	t.Run("skips injection when X-Diverge-Toolbar: off header is sent", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://pr-42.preview.example.com/", nil)
		req.Header.Set("X-Diverge-Toolbar", "off")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, htmlDoc, w.Body.String())
		assert.NotContains(t, w.Body.String(), "/-/toolbar.js")
	})

	t.Run("skips injection on non-HTML or compressed responses", func(t *testing.T) {
		reqJSON := httptest.NewRequest(http.MethodGet, "http://pr-42.preview.example.com/json", nil)
		wJSON := httptest.NewRecorder()
		server.ServeHTTP(wJSON, reqJSON)
		assert.Equal(t, `{"ok":true}`, wJSON.Body.String())

		reqGzip := httptest.NewRequest(http.MethodGet, "http://pr-42.preview.example.com/compressed", nil)
		reqGzip.Header.Set("Accept-Encoding", "gzip")
		wGzip := httptest.NewRecorder()
		server.ServeHTTP(wGzip, reqGzip)
		assert.Equal(t, htmlDoc, wGzip.Body.String())
	})

	t.Run("skips injection when DisableToolbar is true", func(t *testing.T) {
		disabledServer, err := NewServer(Config{
			BaseURL:        backend.URL,
			HeaderKey:      "x-diverge-env",
			PreviewDomain:  "preview.example.com",
			DisableToolbar: true,
		}, lister)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "http://pr-42.preview.example.com/", nil)
		w := httptest.NewRecorder()
		disabledServer.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, htmlDoc, w.Body.String())
	})
}
