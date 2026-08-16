package kafka

import (
	"github.com/divergedev/diverge/pkg/sdk"
)

// Header represents a simple key-value header in Kafka messages.
type Header struct {
	Key   string
	Value []byte
}

// InjectHeaders injects the x-diverge-env header into a list of Kafka headers.
// If envName is empty, it returns the original headers unmodified.
// If the header already exists, its value is updated.
func InjectHeaders(headers []Header, envName string) []Header {
	if envName == "" {
		return headers
	}

	for i, h := range headers {
		if h.Key == sdk.DefaultHeaderKey {
			headers[i].Value = []byte(envName)
			return headers
		}
	}

	return append(headers, Header{
		Key:   sdk.DefaultHeaderKey,
		Value: []byte(envName),
	})
}

// ExtractEnvName extracts the preview environment name from Kafka headers.
func ExtractEnvName(headers []Header) string {
	for _, h := range headers {
		if h.Key == sdk.DefaultHeaderKey {
			return string(h.Value)
		}
	}
	return ""
}
