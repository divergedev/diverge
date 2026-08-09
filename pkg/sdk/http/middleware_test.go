package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divergedev/diverge/pkg/sdk"
	"github.com/stretchr/testify/assert"
)

func TestMiddlewareExtractsHeader(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(DefaultHeaderKey, "pr-123")

	var extractedEnv string
	handler := PropagateEnvironment(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		extractedEnv = sdk.EnvironmentFromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, "pr-123", extractedEnv)
}

func TestMiddlewareNoHeader(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)

	var extractedEnv string
	handler := PropagateEnvironment(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		extractedEnv = sdk.EnvironmentFromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, "", extractedEnv)
}

type mockRoundTripper struct {
	req *http.Request
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	m.req = req
	return &http.Response{}, nil
}

func TestRoundTripperInjectsHeader(t *testing.T) {
	mockRT := &mockRoundTripper{}
	rt := RoundTripper(mockRT)

	ctx := sdk.WithEnvironment(context.Background(), "pr-123")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)

	_, err := rt.RoundTrip(req)
	assert.NoError(t, err)

	assert.Equal(t, "pr-123", mockRT.req.Header.Get(DefaultHeaderKey))
}

func TestRoundTripperNoContext(t *testing.T) {
	mockRT := &mockRoundTripper{}
	rt := RoundTripper(mockRT)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)

	_, err := rt.RoundTrip(req)
	assert.NoError(t, err)

	assert.Equal(t, "", mockRT.req.Header.Get(DefaultHeaderKey))
}
