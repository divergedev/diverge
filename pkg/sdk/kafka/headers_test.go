package kafka_test

import (
	"testing"

	"github.com/divergedev/diverge/pkg/sdk"
	"github.com/divergedev/diverge/pkg/sdk/kafka"
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
}

func TestInjectOverwrite(t *testing.T) {
	headers := []kafka.Header{
		{Key: sdk.GetHeaderKey(), Value: []byte("old-env")},
		{Key: sdk.GetHeaderKey(), Value: []byte("old-env-2")},
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
		envName := rapid.StringMatching(`^[a-zA-Z0-9-]{0,63}$`).Draw(t, "envName")
		if envName == "" {
			if len(kafka.InjectHeaders(nil, "")) != 0 {
				t.Fatalf("Expected no headers")
			}
			return
		}
		injected := kafka.InjectHeaders(nil, envName)
		if kafka.ExtractEnvName(injected) != envName {
			t.Fatalf("Expected envName")
		}
	})
}

func TestProperty_InjectDoesNotMutateInput(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		envName := rapid.StringMatching(`^[a-zA-Z0-9-]{1,63}$`).Draw(t, "envName")
		original := []kafka.Header{{Key: sdk.GetHeaderKey(), Value: []byte("old")}}
		originalCopy := make([]kafka.Header, len(original))
		copy(originalCopy, original)
		kafka.InjectHeaders(original, envName)
		if string(original[0].Value) != string(originalCopy[0].Value) {
			t.Fatalf("Expected original input to not be mutated")
		}
	})
}
