package kafka_test

import (
	"fmt"
	"testing"
	"testing/quick"

	"github.com/divergedev/diverge/pkg/sdk"
	"github.com/divergedev/diverge/pkg/sdk/kafka"
	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

func TestInjectExtractRoundtrip(t *testing.T) {
	headers := []kafka.Header{
		{Key: "other-header", Value: []byte("val")},
	}
	env := "preview-123"

	injected := kafka.InjectHeaders(headers, env)
	extracted := kafka.ExtractEnvName(injected)

	if extracted != env {
		t.Errorf("Expected extracted environment %q, got %q", env, extracted)
	}

	// Test empty env
	injectedEmpty := kafka.InjectHeaders(injected, "")
	if len(injectedEmpty) != len(injected) {
		t.Errorf("Expected inject with empty env to not modify headers")
	}
}

func TestInjectOverwrite(t *testing.T) {
	headers := []kafka.Header{
		{Key: sdk.GetHeaderKey(), Value: []byte("old-env")},
	}
	env := "new-env"

	injected := kafka.InjectHeaders(headers, env)

	if len(injected) != 1 {
		t.Errorf("Expected exactly 1 header, got %d", len(injected))
	}

	extracted := kafka.ExtractEnvName(injected)
	if extracted != env {
		t.Errorf("Expected extracted environment %q, got %q", env, extracted)
	}
}

func TestProperty_InjectExtractRoundtrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		envName := rapid.StringMatching(`[a-z][a-z0-9-]{0,20}`).Draw(t, "envName")
		// Generate random initial headers, potentially with duplicates of the target key
		numHeaders := rapid.IntRange(0, 5).Draw(t, "numHeaders")
		initial := make([]kafka.Header, numHeaders)
		for i := range initial {
			initial[i] = kafka.Header{
				Key:   rapid.SampledFrom([]string{sdk.DefaultHeaderKey, "other-key", "x-request-id"}).Draw(t, fmt.Sprintf("key%d", i)),
				Value: []byte(rapid.String().Draw(t, fmt.Sprintf("val%d", i))),
			}
		}

		result := kafka.InjectHeaders(initial, envName)
		extracted := kafka.ExtractEnvName(result)

		if envName == "" {
			// Should be unchanged
		} else {
			assert.Equal(t, envName, extracted)
			// Count occurrences of target key — must be exactly 1
			count := 0
			for _, h := range result {
				if h.Key == sdk.GetHeaderKey() {
					count++
				}
			}
			assert.Equal(t, 1, count, "must have exactly one env header")
		}
	})
}

func TestProperty_InjectDoesNotMutateInput(t *testing.T) {
	f := func(envName string) bool {
		if envName == "" {
			return true
		}
		original := []kafka.Header{{Key: sdk.GetHeaderKey(), Value: []byte("old")}}
		originalCopy := make([]kafka.Header, len(original))
		copy(originalCopy, original)
		kafka.InjectHeaders(original, envName)
		return string(original[0].Value) == string(originalCopy[0].Value)
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}
