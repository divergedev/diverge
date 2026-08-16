package kafka

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

func TestHeadersSdk(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		expected map[string]string
	}{
		{
			name:     "empty env",
			env:      "",
			expected: map[string]string{},
		},
		{
			name:     "with env",
			env:      "pr-123",
			expected: map[string]string{HeaderKey: "pr-123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DIVERGE_ENV", tt.env)

			headers := Headers()
			assert.Equal(t, tt.expected, headers)
		})
	}
}

func TestTopicAndConsumerGroupSdk(t *testing.T) {
	t.Setenv("DIVERGE_ENV", "pr-123")

	assert.Equal(t, "my-topic-pr-123", Topic("my-topic"))
	assert.Equal(t, "my-group-pr-123", ConsumerGroup("my-group"))

	t.Setenv("DIVERGE_ENV", "")
	assert.Equal(t, "my-topic", Topic("my-topic"))
	assert.Equal(t, "my-group", ConsumerGroup("my-group"))
}

func TestTopicAndConsumerGroupSdk_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		base := rapid.StringMatching(`^[a-z0-9-]+$`).Draw(t, "base")
		env := rapid.StringMatching(`^[a-z0-9-]+$`).Draw(t, "env")

		//nolint:errcheck
		os.Setenv("DIVERGE_ENV", env)
		defer os.Unsetenv("DIVERGE_ENV") //nolint:errcheck

		expected := base + "-" + env
		assert.Equal(t, expected, Topic(base))
		assert.Equal(t, expected, ConsumerGroup(base))
	})
}
