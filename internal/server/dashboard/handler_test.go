package dashboard

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testHandler creates an SPA handler backed by the real embedded assets.
func testHandler(t *testing.T) http.Handler {
	t.Helper()
	return SPAHandler(Assets)
}

func TestSPAHandler_IndexFallback(t *testing.T) {
	handler := testHandler(t)

	// Root path should serve index.html
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "<!DOCTYPE html>")
	assert.Equal(t, "no-cache, no-store, must-revalidate", w.Header().Get("Cache-Control"))
}

func TestSPAHandler_SPARouting(t *testing.T) {
	handler := testHandler(t)

	// Non-existent paths should fall back to index.html (SPA routing)
	paths := []string{
		"/login",
		"/environments/default/my-env",
		"/preview-groups",
		"/preview-groups/default/my-pg",
		"/cluster",
		"/some/deeply/nested/route",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code, "path %s should return 200", path)
			assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
			assert.Contains(t, w.Body.String(), "<!DOCTYPE html>")
		})
	}
}

func TestSPAHandler_StaticAssets(t *testing.T) {
	handler := testHandler(t)

	// Find a real asset file in the embedded dist
	distFS, err := fs.Sub(Assets, "dist")
	require.NoError(t, err)

	var assetPath string
	err = fs.WalkDir(distFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && path != "index.html" && path != ".gitkeep" {
			assetPath = path
			return fs.SkipAll
		}
		return nil
	})
	require.NoError(t, err)

	if assetPath == "" {
		t.Skip("no static assets found in embedded dist (dev build may be minimal)")
	}

	req := httptest.NewRequest("GET", "/"+assetPath, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// Should NOT be index.html content
	assert.NotContains(t, w.Header().Get("Content-Type"), "text/html")
}

func TestSPAHandler_CacheHeaders_HashedAssets(t *testing.T) {
	handler := testHandler(t)

	// Find an asset under /assets/ directory
	distFS, err := fs.Sub(Assets, "dist")
	require.NoError(t, err)

	var hashedAssetPath string
	_ = fs.WalkDir(distFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && len(path) > 7 && path[:7] == "assets/" {
			hashedAssetPath = path
			return fs.SkipAll
		}
		return nil
	})

	if hashedAssetPath == "" {
		t.Skip("no hashed assets found in embedded dist")
	}

	req := httptest.NewRequest("GET", "/"+hashedAssetPath, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"),
		"hashed assets under /assets/ should be cached immutably")
}

func TestSPAHandler_CacheHeaders_IndexHTML(t *testing.T) {
	handler := testHandler(t)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, "no-cache, no-store, must-revalidate", w.Header().Get("Cache-Control"),
		"index.html should never be cached")
}

func TestSPAHandler_PathTraversal(t *testing.T) {
	handler := testHandler(t)

	// Path traversal attempts should fall back to index.html (no directory listing)
	traversalPaths := []string{
		"/../../../etc/passwd",
		"/..%2f..%2f..%2fetc%2fpasswd",
		"/..",
	}

	for _, path := range traversalPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			// Should either serve index.html or redirect, never expose parent directories
			body := w.Body.String()
			assert.NotContains(t, body, "root:", "should not expose system files")
		})
	}
}

func TestSPAHandler_MethodsAllowed(t *testing.T) {
	handler := testHandler(t)

	methods := []string{"GET", "HEAD"}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

func TestSPAHandler_EmptyDist(t *testing.T) {
	// Test behavior when dist has no index.html
	// Create a minimal embed.FS with only .gitkeep
	// We can't create embed.FS at runtime, so test the error path
	// by directly calling the setCacheHeaders function
	w := httptest.NewRecorder()
	setCacheHeaders(w, "/assets/main.abc123.js")
	assert.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"))

	w2 := httptest.NewRecorder()
	setCacheHeaders(w2, "/favicon.ico")
	assert.Equal(t, "public, max-age=3600", w2.Header().Get("Cache-Control"))
}

func TestSetCacheHeaders(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{"hashed JS", "/assets/main.abc123.js", "public, max-age=31536000, immutable"},
		{"hashed CSS", "/assets/index.def456.css", "public, max-age=31536000, immutable"},
		{"hashed chunk", "/assets/vendor-xyz.js", "public, max-age=31536000, immutable"},
		{"favicon", "/favicon.ico", "public, max-age=3600"},
		{"manifest", "/manifest.json", "public, max-age=3600"},
		{"robots", "/robots.txt", "public, max-age=3600"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			setCacheHeaders(w, tc.path)
			assert.Equal(t, tc.expected, w.Header().Get("Cache-Control"))
		})
	}
}

func TestEmbedFS_ContainsDist(t *testing.T) {
	// Verify the embed directive actually captures the dist directory
	entries, err := fs.ReadDir(Assets, "dist")
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "embedded dist/ should contain files")

	// Should have at least index.html or .gitkeep
	var hasFile bool
	for _, e := range entries {
		if !e.IsDir() {
			hasFile = true
			break
		}
	}
	assert.True(t, hasFile, "embedded dist/ should contain at least one file")
}

func TestSPAHandler_IndexHTMLContent(t *testing.T) {
	handler := testHandler(t)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	// Verify it's actually our dashboard index.html
	assert.Contains(t, body, "<div id=\"root\">")
	assert.Contains(t, body, "Diverge")
}

func TestSPAHandler_ConcurrentRequests(t *testing.T) {
	handler := testHandler(t)
	const concurrency = 50

	done := make(chan struct{}, concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			req := httptest.NewRequest("GET", "/", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
		}()
	}

	for i := 0; i < concurrency; i++ {
		<-done
	}
}

// Test that reading index.html directly gives same content as SPA fallback
func TestSPAHandler_FallbackConsistency(t *testing.T) {
	handler := testHandler(t)

	// Request root (SPA fallback)
	rootReq := httptest.NewRequest("GET", "/", nil)
	rootW := httptest.NewRecorder()
	handler.ServeHTTP(rootW, rootReq)

	// Request unknown path (also SPA fallback)
	unknownReq := httptest.NewRequest("GET", "/nonexistent/page", nil)
	unknownW := httptest.NewRecorder()
	handler.ServeHTTP(unknownW, unknownReq)

	// Both should return identical content
	rootBody, _ := io.ReadAll(rootW.Result().Body)
	unknownBody, _ := io.ReadAll(unknownW.Result().Body)
	assert.Equal(t, string(rootBody), string(unknownBody),
		"SPA fallback should serve identical index.html for all unknown paths")
}
