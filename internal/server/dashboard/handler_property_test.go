package dashboard

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

func TestSPAHandler_PBT_AnyPathReturns200(t *testing.T) {
	handler := SPAHandler(Assets)

	rapid.Check(t, func(t *rapid.T) {
		// Generate random URL paths (1-5 segments of lowercase alpha)
		segments := rapid.IntRange(1, 5).Draw(t, "numSegments")
		path := ""
		for i := 0; i < segments; i++ {
			segment := rapid.StringMatching(`^[a-z0-9_-]{1,20}$`).Draw(t, "segment")
			path += "/" + segment
		}

		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		// Every valid path should return 200 (either a real file or SPA fallback)
		assert.Equal(t, http.StatusOK, w.Code,
			"path %q should return 200", path)

		// Content-Type should always be set
		ct := w.Header().Get("Content-Type")
		assert.NotEmpty(t, ct, "Content-Type should be set for path %q", path)
	})
}

func TestSPAHandler_PBT_UnknownPathServesIndexHTML(t *testing.T) {
	handler := SPAHandler(Assets)

	// Pre-read index.html to compare against
	distFS, err := fs.Sub(Assets, "dist")
	if err != nil {
		t.Fatalf("failed to get dist sub-fs: %v", err)
	}
	indexContent, err := fs.ReadFile(distFS, "index.html")
	if err != nil {
		t.Skip("no index.html in embedded dist")
	}

	// Collect known files so we can generate unknown paths
	knownFiles := make(map[string]bool)
	_ = fs.WalkDir(distFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			knownFiles["/"+path] = true
		}
		return nil
	})

	rapid.Check(t, func(t *rapid.T) {
		// Generate paths that DON'T match any real file
		path := "/" + rapid.StringMatching(`^app/[a-z]{3,10}/[a-z]{3,10}$`).Draw(t, "path")

		// Verify path is not a known file
		if knownFiles[path] {
			return // skip this case
		}

		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
		assert.Equal(t, string(indexContent), w.Body.String(),
			"unknown path %q should serve index.html", path)
	})
}

func TestSPAHandler_PBT_CacheHeaderInvariants(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		path := rapid.SampledFrom([]string{
			"/assets/main.abc123.js",
			"/assets/index.def456.css",
			"/assets/vendor-xyz.js",
			"/assets/chunk-123.js",
		}).Draw(t, "assetPath")

		w := httptest.NewRecorder()
		setCacheHeaders(w, path)

		// All /assets/* paths MUST get immutable cache
		assert.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"),
			"asset path %q must be immutable", path)
	})
}

func TestSPAHandler_PBT_NonAssetPathsGetShortCache(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate paths that DON'T start with /assets/
		filename := rapid.StringMatching(`^[a-z]{1,10}\.[a-z]{2,4}$`).Draw(t, "filename")
		path := "/" + filename

		w := httptest.NewRecorder()
		setCacheHeaders(w, path)

		assert.Equal(t, "public, max-age=3600", w.Header().Get("Cache-Control"),
			"non-asset path %q should get short cache", path)
	})
}

func TestSPAHandler_PBT_RootPathNeverCrashes(t *testing.T) {
	handler := SPAHandler(Assets)

	rapid.Check(t, func(t *rapid.T) {
		method := rapid.SampledFrom([]string{
			"GET", "HEAD", "POST", "PUT", "DELETE", "PATCH", "OPTIONS",
		}).Draw(t, "method")

		req := httptest.NewRequest(method, "/", nil)
		w := httptest.NewRecorder()

		// Should never panic regardless of HTTP method
		assert.NotPanics(t, func() {
			handler.ServeHTTP(w, req)
		})
	})
}
