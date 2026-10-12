package proxy

import (
	"net/http"
	"net/url"
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

// isAllowedOrigin checks if the origin is a valid HTTP(S) origin whose host matches or is a subdomain of previewDomain.
func isAllowedOrigin(origin, previewDomain string) bool {
	if origin == "" || previewDomain == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.User != nil {
		return false
	}
	if u.Path != "" && u.Path != "/" {
		return false
	}
	if u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(origin, "?") || strings.HasSuffix(origin, "#") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	pd := strings.ToLower(previewDomain)
	return host == pd || strings.HasSuffix(host, "."+pd)
}
