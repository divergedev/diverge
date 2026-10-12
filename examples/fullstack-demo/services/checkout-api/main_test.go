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

func TestCheckoutHandler(t *testing.T) {
	t.Setenv("DIVERGE_ENV", "pr-42")
	t.Setenv("DIVERGE_PREVIEW_SCHEMA", "preview_pr_42")
	t.Setenv("DATABASE_URL", "postgres://admin:supersecret@postgres.default.svc:5432/orders?search_path=preview_pr_42")
	t.Setenv("ORDERS_QUEUE_TOPIC", "orders-events-pr-42")
	t.Setenv("FEATURE_INSTANT_PAYOUTS", "true")
	t.Setenv("FEATURE_CHECKOUT_V2_BANNER", "true")

	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/enqueue", r.URL.Path)
		assert.Equal(t, "pr-42", r.Header.Get("x-diverge-env"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"service":"fulfillment-worker","environment":"baseline","receivedPreviewHeader":"pr-42"}`))
	}))
	defer worker.Close()

	handler := NewCheckoutHandler(worker.URL, worker.Client())

	req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	req.Header.Set("x-diverge-env", "pr-42")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp fullstackdemo.CheckoutResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "checkout-api", resp.Service)
	assert.Equal(t, "pr-42", resp.Environment)
	assert.Equal(t, "preview_pr_42", resp.DatabaseSchema)
	assert.True(t, resp.DatabaseConfigured)
	assert.NotContains(t, resp.DatabaseURL, "supersecret")
	assert.True(t, resp.InstantPayoutsEnabled)
	assert.True(t, resp.CheckoutV2Banner)
	assert.Equal(t, "orders-events-pr-42", resp.OrdersQueueTopic)
	assert.Equal(t, "pr-42", resp.ReceivedPreviewHeader)
}
