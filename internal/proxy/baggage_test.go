package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/baggage"
	"hegel.dev/go/hegel"
)

func TestBaggageExtractor_PreviewHeaders(t *testing.T) {
	var captured *http.Request
	handler := BaggageExtractorMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-preview-env", "staging")
	req.Header.Set("x-tenant-id", "t1")

	handler.ServeHTTP(httptest.NewRecorder(), req)

	if captured == nil {
		t.Fatal("handler not called")
	}

	bag := baggage.FromContext(captured.Context())
	if bag.Member("x-preview-env").Value() != "staging" {
		t.Errorf("expected x-preview-env=staging, got %s", bag.Member("x-preview-env").Value())
	}
	if bag.Member("x-tenant-id").Value() != "t1" {
		t.Errorf("expected x-tenant-id=t1, got %s", bag.Member("x-tenant-id").Value())
	}
}

func TestBaggageExtractor_NoHeaders(t *testing.T) {
	var captured *http.Request
	handler := BaggageExtractorMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("x-other-header", "foo")

	handler.ServeHTTP(httptest.NewRecorder(), req)

	bag := baggage.FromContext(captured.Context())
	if bag.Len() != 0 {
		t.Errorf("expected empty baggage, got %v", bag)
	}
}

func TestBaggageExtractor_PassesThrough(t *testing.T) {
	called := false
	handler := BaggageExtractorMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !called {
		t.Error("expected handler to be called")
	}
}

func TestBaggageExtractor_AnyHeader_PBT(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		genAlphaNum := func() string {
			length := hegel.Draw(ht, hegel.Integers(1, 15))
			const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
			var sb strings.Builder
			for i := 0; i < length; i++ {
				sb.WriteByte(charset[hegel.Draw(ht, hegel.Integers(0, len(charset)-1))])
			}
			return sb.String()
		}

		val := genAlphaNum()
		headerKey := "x-preview-" + genAlphaNum()

		var captured *http.Request
		handler := BaggageExtractorMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			captured = r
		}))

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set(headerKey, val)

		handler.ServeHTTP(httptest.NewRecorder(), req)

		bag := baggage.FromContext(captured.Context())
		lowerKey := strings.ToLower(headerKey)
		if bag.Member(lowerKey).Value() != val {
			t.Fatalf("expected baggage %s=%s, got %s", lowerKey, val, bag.Member(lowerKey).Value())
		}
	})
}
