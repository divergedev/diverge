package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

type mockProvider struct {
	user  *UserInfo
	err   error
	calls int
}

func (m *mockProvider) Authenticate(ctx context.Context, token string) (*UserInfo, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	if m.user != nil {
		return m.user, nil
	}
	return &UserInfo{Username: token, UID: "fallback-uid", Extra: map[string]authorizationv1.ExtraValue{"default": {"val"}}}, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testMetrics() *AuthMetrics {
	return &AuthMetrics{
		Latency:     prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "auth_latency"}, []string{"provider", "result"}),
		CacheHits:   prometheus.NewCounter(prometheus.CounterOpts{Name: "auth_cache_hits"}),
		CacheMisses: prometheus.NewCounter(prometheus.CounterOpts{Name: "auth_cache_misses"}),
		Attempts:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "auth_attempts"}, []string{"provider", "result"}),
	}
}

func TestMiddleware_ExemptPaths(t *testing.T) {
	cfg := MiddlewareConfig{
		Provider:    &mockProvider{},
		Cache:       NewTokenCache(10, time.Minute),
		Logger:      testLogger(),
		ExemptPaths: []string{"/healthz"},
	}
	mw := NewMiddleware(cfg)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func TestMiddleware_ExemptPrefixes(t *testing.T) {
	cfg := MiddlewareConfig{
		Provider:       &mockProvider{},
		Cache:          NewTokenCache(10, time.Minute),
		Logger:         testLogger(),
		ExemptPrefixes: []string{"/assets/"},
	}
	mw := NewMiddleware(cfg)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{"asset JS bypasses auth", "/assets/main.abc123.js", http.StatusOK},
		{"asset CSS bypasses auth", "/assets/index.def456.css", http.StatusOK},
		{"nested asset bypasses auth", "/assets/chunks/vendor.js", http.StatusOK},
		{"non-asset requires auth", "/api/v1/environments", http.StatusUnauthorized},
		{"root requires auth", "/", http.StatusUnauthorized},
		{"similar prefix requires auth", "/asset-not-assets/file.js", http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			assert.Equal(t, tc.wantStatus, w.Result().StatusCode)
		})
	}
}

func TestMiddleware_ExemptPrefixes_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prefix := "/" + rapid.StringMatching(`^[a-z]{2,10}/$`).Draw(t, "prefix")
		filename := rapid.StringMatching(`^[a-z0-9._-]{1,30}$`).Draw(t, "filename")

		cfg := MiddlewareConfig{
			Provider:       &mockProvider{},
			Cache:          NewTokenCache(100, time.Minute),
			Logger:         testLogger(),
			ExemptPrefixes: []string{prefix},
		}
		mw := NewMiddleware(cfg)
		handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		// Path WITH the prefix should bypass auth
		req := httptest.NewRequest("GET", prefix+filename, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code,
			"path %q should bypass auth with prefix %q", prefix+filename, prefix)

		// Path WITHOUT the prefix should require auth
		otherPath := "/other/" + filename
		req2 := httptest.NewRequest("GET", otherPath, nil)
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, req2)
		assert.Equal(t, http.StatusUnauthorized, w2.Code,
			"path %q should require auth (prefix is %q)", otherPath, prefix)
	})
}

func TestMiddleware_MissingToken(t *testing.T) {
	cfg := MiddlewareConfig{
		Provider: &mockProvider{},
		Cache:    NewTokenCache(10, time.Minute),
		Logger:   testLogger(),
	}
	mw := NewMiddleware(cfg)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Result().StatusCode)
}

func TestMiddleware_EmptyBearerToken(t *testing.T) {
	cfg := MiddlewareConfig{
		Provider: &mockProvider{},
		Cache:    NewTokenCache(10, time.Minute),
		Logger:   testLogger(),
	}
	mw := NewMiddleware(cfg)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Result().StatusCode)
}

func TestMiddleware_SuccessfulAuth(t *testing.T) {
	user := &UserInfo{Username: "test-user", UID: "uid-123", Extra: map[string]authorizationv1.ExtraValue{"k1": {"v1"}}}
	cfg := MiddlewareConfig{
		Provider: &mockProvider{user: user},
		Cache:    NewTokenCache(10, time.Minute),
		Logger:   testLogger(),
	}
	mw := NewMiddleware(cfg)

	var ctxUser *UserInfo
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxUser, _ = UserInfoFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.NotNil(t, ctxUser)
	assert.Equal(t, user.Username, ctxUser.Username)
	assert.Equal(t, user.UID, ctxUser.UID)
	assert.Equal(t, user.Extra, ctxUser.Extra)
}

func TestMiddleware_FailedAuth(t *testing.T) {
	cfg := MiddlewareConfig{
		Provider: &mockProvider{err: errors.New("invalid")},
		Cache:    NewTokenCache(10, time.Minute),
		Logger:   testLogger(),
	}
	mw := NewMiddleware(cfg)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Result().StatusCode)
}

func TestMiddleware_CacheHit(t *testing.T) {
	provider := &mockProvider{user: &UserInfo{Username: "cached-user", UID: "cached-uid", Extra: map[string]authorizationv1.ExtraValue{"cache": {"hit"}}}}
	cfg := MiddlewareConfig{
		Provider: provider,
		Cache:    NewTokenCache(10, time.Minute),
		Logger:   testLogger(),
	}
	mw := NewMiddleware(cfg)
	var ctxUser *UserInfo
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxUser, _ = UserInfoFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	// First call
	req1 := httptest.NewRequest("GET", "/api", nil)
	req1.Header.Set("Authorization", "Bearer my-token")
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Result().StatusCode)
	assert.Equal(t, 1, provider.calls)

	// Second call
	req2 := httptest.NewRequest("GET", "/api", nil)
	req2.Header.Set("Authorization", "Bearer my-token")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Result().StatusCode)
	assert.Equal(t, 1, provider.calls) // Still 1 call

	require.NotNil(t, ctxUser)
	assert.Equal(t, "cached-uid", ctxUser.UID)
	assert.Equal(t, map[string]authorizationv1.ExtraValue{"cache": {"hit"}}, ctxUser.Extra)
}

func TestMiddleware_CacheExpiry(t *testing.T) {
	provider := &mockProvider{user: &UserInfo{Username: "cached-user", UID: "cached-uid", Extra: map[string]authorizationv1.ExtraValue{"cache": {"hit"}}}}
	cfg := MiddlewareConfig{
		Provider: provider,
		Cache:    NewTokenCache(10, time.Millisecond*50),
		Logger:   testLogger(),
	}
	mw := NewMiddleware(cfg)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("Authorization", "Bearer my-token")

	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req)
	assert.Equal(t, 1, provider.calls)

	time.Sleep(time.Millisecond * 60)

	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req)
	assert.Equal(t, 2, provider.calls) // Provider called again
}

func TestMiddleware_MetricsIncremented(t *testing.T) {
	metrics := testMetrics()
	cfg := MiddlewareConfig{
		Provider: &mockProvider{user: &UserInfo{Username: "user", UID: "user-uid", Extra: map[string]authorizationv1.ExtraValue{"cache": {"hit"}}}},
		Cache:    NewTokenCache(10, time.Minute),
		Logger:   testLogger(),
		Metrics:  metrics,
	}
	mw := NewMiddleware(cfg)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 1: Missing token -> Attempt none/failure
	req := httptest.NewRequest("GET", "/api", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, float64(1), testutil.ToFloat64(metrics.Attempts.WithLabelValues("none", "failure")))

	// 2: Valid token -> Cache miss, TokenReview success
	req.Header.Set("Authorization", "Bearer valid-token")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, float64(1), testutil.ToFloat64(metrics.CacheMisses))
	assert.Equal(t, float64(1), testutil.ToFloat64(metrics.Attempts.WithLabelValues("tokenreview", "success")))

	// 3: Valid token again -> Cache hit
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, float64(1), testutil.ToFloat64(metrics.CacheHits))
	assert.Equal(t, float64(1), testutil.ToFloat64(metrics.Attempts.WithLabelValues("cache", "success")))
}

func TestMiddleware_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		tokenGen := rapid.StringMatching(`^[a-zA-Z0-9_-]{1,100}$`)
		token := tokenGen.Draw(t, "token")
		shouldFail := rapid.Bool().Draw(t, "shouldFail")

		var err error
		if shouldFail {
			err = errors.New("auth failed")
		}

		provider := &mockProvider{err: err}
		cfg := MiddlewareConfig{
			Provider: provider,
			Cache:    NewTokenCache(100, time.Minute),
			Logger:   testLogger(),
		}
		mw := NewMiddleware(cfg)

		var ctxUser *UserInfo
		handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctxUser, _ = UserInfoFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/api", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if shouldFail {
			assert.Equal(t, http.StatusUnauthorized, w.Result().StatusCode)
			assert.Nil(t, ctxUser)
		} else {
			assert.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.NotNil(t, ctxUser)
			assert.Equal(t, token, ctxUser.Username)
			assert.Equal(t, "fallback-uid", ctxUser.UID)
			assert.Equal(t, map[string]authorizationv1.ExtraValue{"default": {"val"}}, ctxUser.Extra)
		}
	})
}
