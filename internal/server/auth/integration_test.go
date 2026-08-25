package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiddleware_CompositeProvider_SessionCookie_Integration(t *testing.T) {
	sm, err := NewSessionManager(SessionConfig{})
	require.NoError(t, err)

	provider := &OIDCProvider{
		session:       sm,
		allowedGroups: map[string]bool{},
	}

	cp := NewCompositeProvider(testLogger())
	cp.Add("oidc", provider)

	cfg := MiddlewareConfig{
		Provider: cp,
		Cache:    NewTokenCache(10, time.Minute),
		Logger:   testLogger(),
	}
	mw := NewMiddleware(cfg)

	var ctxUser *UserInfo
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxUser, _ = UserInfoFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	token, err := sm.Mint("int-user", "integration@example.com", "oidc", []string{"dev"})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api", nil)
	req.AddCookie(&http.Cookie{Name: "diverge_token", Value: token})

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.NotNil(t, ctxUser)
	assert.Equal(t, "int-user", ctxUser.Username)
	assert.Equal(t, "integration@example.com", ctxUser.Email)
	assert.Equal(t, []string{"dev"}, ctxUser.Groups)
}
