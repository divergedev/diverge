package proxy

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/baggage"
)

// BaggageExtractorMiddleware reads x-preview-* and x-tenant-id headers
// and injects them as W3C Baggage entries so they appear in OTel spans.
func BaggageExtractorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var members []baggage.Member
		for name, values := range r.Header {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "x-preview-") || lower == "x-tenant-id" || lower == "x-request-id" {
				if len(values) > 0 {
					m, err := baggage.NewMember(lower, values[0])
					if err == nil {
						members = append(members, m)
					}
				}
			}
		}
		if len(members) > 0 {
			bag, err := baggage.New(members...)
			if err == nil {
				r = r.WithContext(baggage.ContextWithBaggage(r.Context(), bag))
			}
		}
		next.ServeHTTP(w, r)
	})
}
