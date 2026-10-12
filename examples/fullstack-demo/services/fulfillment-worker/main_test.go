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

func TestWorkerHandler(t *testing.T) {
	t.Setenv("DIVERGE_ENV", "baseline")
	t.Setenv("ORDERS_QUEUE_TOPIC", "orders-events")
	t.Setenv("CONSUMER_GROUP_ID", "orders-cg-baseline")

	handler := NewWorkerHandler()

	req := httptest.NewRequest(http.MethodPost, "/enqueue", nil)
	req.Header.Set("x-diverge-env", "pr-42")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp fullstackdemo.WorkerResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "fulfillment-worker", resp.Service)
	assert.Equal(t, "baseline", resp.Environment)
	assert.Equal(t, "orders-events", resp.OrdersQueueTopic)
	assert.Equal(t, "orders-cg-baseline", resp.ConsumerGroup)
	assert.Equal(t, "pr-42", resp.ReceivedPreviewHeader)
}
