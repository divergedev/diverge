package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// SPAHandler returns an http.Handler that serves the embedded dashboard assets
// with SPA (Single Page Application) routing support.
func SPAHandler(assets embed.FS) http.Handler {
	distFS, err := fs.Sub(assets, "dist")
	if err != nil {
		panic("dashboard: failed to create sub filesystem: " + err.Error())
	}

	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urlPath := path.Clean(r.URL.Path)
		if urlPath == "." {
			urlPath = "/"
		}

		if urlPath != "/" {
			filePath := strings.TrimPrefix(urlPath, "/")
			if _, err := fs.Stat(distFS, filePath); err == nil {
				setCacheHeaders(w, urlPath)
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		indexContent, err := fs.ReadFile(distFS, "index.html")
		if err != nil {
			http.Error(w, "dashboard not available", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(indexContent)
	})
}

func setCacheHeaders(w http.ResponseWriter, urlPath string) {
	if strings.HasPrefix(urlPath, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
}
