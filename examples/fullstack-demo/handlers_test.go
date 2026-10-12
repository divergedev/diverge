package fullstackdemo

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaskDatabaseURL(t *testing.T) {
	assert.Equal(t, "", maskDatabaseURL(""))
	assert.Equal(t, "configured", maskDatabaseURL("not-a-valid-url"))
	assert.Equal(
		t,
		"postgres://diverge_user:****@postgres:5432/shop?sslmode=disable",
		maskDatabaseURL("postgres://diverge_user:s3cr3t@postgres:5432/shop?sslmode=disable"),
	)
	assert.Equal(
		t,
		"postgres://diverge_user@postgres:5432/shop?password=****&sslmode=require",
		maskDatabaseURL("postgres://diverge_user@postgres:5432/shop?password=supersecret&sslmode=require"),
	)
}

func TestRenderStorefrontHTML_EscapesUntrustedHeaders(t *testing.T) {
	rendered := renderStorefrontHTML("<script>alert(1)</script>", `pr-42"><img src=x onerror=alert(1)>`)
	assert.NotContains(t, rendered, "<script>alert(1)</script>")
	assert.NotContains(t, rendered, `<img src=x onerror=alert(1)>`)
	assert.Contains(t, rendered, "&lt;script&gt;alert(1)&lt;/script&gt;")
	assert.Contains(t, rendered, "pr-42&#34;&gt;&lt;img src=x onerror=alert(1)&gt;")
}

func TestHandlers_DownstreamErrorAndMalformedJSONHandling(t *testing.T) {
	t.Run("checkout-api rejects non-2xx from fulfillment-worker", func(t *testing.T) {
		workerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "worker overloaded", http.StatusServiceUnavailable)
		}))
		defer workerSrv.Close()

		handler := NewCheckoutHandler(workerSrv.URL, workerSrv.Client())
		req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusBadGateway, rec.Code)
		assert.Contains(t, rec.Body.String(), "fulfillment-worker returned status 503")
	})

	t.Run("checkout-api rejects malformed JSON from fulfillment-worker", func(t *testing.T) {
		workerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not-json"))
		}))
		defer workerSrv.Close()

		handler := NewCheckoutHandler(workerSrv.URL, workerSrv.Client())
		req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusBadGateway, rec.Code)
		assert.Contains(t, rec.Body.String(), "fulfillment-worker returned malformed JSON")
	})

	t.Run("storefront-web rejects malformed JSON from checkout-api", func(t *testing.T) {
		checkoutSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{invalid-json"))
		}))
		defer checkoutSrv.Close()

		handler := NewStorefrontHandler(checkoutSrv.URL, checkoutSrv.Client())
		req := httptest.NewRequest(http.MethodGet, "/api/checkout", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusBadGateway, rec.Code)
		assert.Contains(t, rec.Body.String(), "checkout-api returned malformed JSON")
	})
}
