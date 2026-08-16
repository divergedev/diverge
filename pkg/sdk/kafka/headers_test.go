package kafka_test

import (
	"testing"
	"testing/quick"

	"github.com/divergedev/diverge/pkg/sdk"
	"github.com/divergedev/diverge/pkg/sdk/kafka"
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
		{Key: sdk.DefaultHeaderKey, Value: []byte("old-env")},
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
	f := func(envName string) bool {
		if envName == "" {
			return len(kafka.InjectHeaders(nil, "")) == 0
		}
		injected := kafka.InjectHeaders(nil, envName)
		return kafka.ExtractEnvName(injected) == envName
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

func TestProperty_InjectDoesNotMutateInput(t *testing.T) {
	f := func(envName string) bool {
		if envName == "" {
			return true
		}
		original := []kafka.Header{{Key: sdk.DefaultHeaderKey, Value: []byte("old")}}
		originalCopy := make([]kafka.Header, len(original))
		copy(originalCopy, original)
		kafka.InjectHeaders(original, envName)
		return string(original[0].Value) == string(originalCopy[0].Value)
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}
