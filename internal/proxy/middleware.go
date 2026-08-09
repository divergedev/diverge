package proxy

import (
	"net/http"
	"strings"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

var mwLogger = ctrl.Log.WithName("proxy").WithName("middleware")

// LoggingMiddleware wraps an http.Handler to log each request's method, path,
// and duration using the structured logger.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		mwLogger.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).String())
	})
}

// CORSMiddleware validates the request Origin against the preview domain.
// Only origins ending with the previewDomain are trusted.
func CORSMiddleware(previewDomain string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && isAllowedOrigin(origin, previewDomain) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Diverge-Env")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isAllowedOrigin checks if the origin's host ends with the preview domain.
func isAllowedOrigin(origin, previewDomain string) bool {
	if previewDomain == "" {
		return false
	}
	// Extract host from origin (e.g., "https://mr-42.preview.example.com" → "mr-42.preview.example.com")
	host := origin
	if idx := strings.Index(origin, "://"); idx != -1 {
		host = origin[idx+3:]
	}
	// Strip port if present
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return host == previewDomain || strings.HasSuffix(host, "."+previewDomain)
}
