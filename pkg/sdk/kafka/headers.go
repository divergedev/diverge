package kafka

import (
	"github.com/divergedev/diverge/pkg/sdk"
)

// Header represents a simple key-value header in Kafka messages.
type Header struct {
	Key   string
	Value []byte
}

// InjectHeaders injects the preview environment header into a list of Kafka headers.
// If envName is empty, it returns the original headers unmodified.
// Removes ALL existing environment headers before appending to prevent duplicate injection bypass.
func InjectHeaders(headers []Header, envName string) []Header {
	if envName == "" {
		return headers
	}

	targetKey := sdk.GetHeaderKey()
	filtered := make([]Header, 0, len(headers))
	for _, h := range headers {
		if h.Key != targetKey {
			filtered = append(filtered, h)
		}
	}

	return append(filtered, Header{
		Key:   targetKey,
		Value: []byte(envName),
	})
}

// ExtractEnvName extracts the preview environment name from Kafka headers.
func ExtractEnvName(headers []Header) string {
	targetKey := sdk.GetHeaderKey()
	for _, h := range headers {
		if h.Key == targetKey {
			return string(h.Value)
		}
	}
	return ""
}
