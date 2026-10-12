package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	fullstackdemo "github.com/divergedev/diverge/examples/fullstack-demo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorefrontHandler(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/checkout", r.URL.Path)
		assert.Equal(t, "pr-42", r.Header.Get("x-diverge-env"))
		assert.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", r.Header.Get("traceparent"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"service":"checkout-api","environment":"pr-42"}`))
	}))
	defer upstream.Close()

	handler := NewStorefrontHandler(upstream.URL, upstream.Client())

	t.Run("healthz", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp fullstackdemo.HealthResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, "ok", resp.Status)
		assert.Equal(t, "storefront-web", resp.Service)
	})

	t.Run("root html page", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("x-diverge-env", "pr-42")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "Diverge Demo Storefront")
		assert.Contains(t, rec.Body.String(), "pr-42")
	})

	t.Run("checkout propagates headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/checkout", nil)
		req.Header.Set("x-diverge-env", "pr-42")
		req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp fullstackdemo.StorefrontResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, "baseline", resp.StorefrontEnv)
		assert.Equal(t, "pr-42", resp.PropagatedHeader)
	})
}
