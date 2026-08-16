package kafka

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestTopic(t *testing.T) {
	topic, err := Topic("orders", "")
	assert.NoError(t, err)
	assert.Equal(t, "orders", topic)

	topic, err = Topic("orders", "preview-1")
	assert.NoError(t, err)
	assert.Equal(t, "orders--preview-1", topic)

	_, err = Topic("my--orders", "preview-1")
	assert.ErrorIs(t, err, ErrInvalidTopic)
}

func TestConsumerGroupSdk(t *testing.T) {
	t.Setenv("DIVERGE_ENV", "pr-123")
	assert.Equal(t, "my-group-pr-123", ConsumerGroup("my-group"))

	t.Setenv("DIVERGE_ENV", "")
	assert.Equal(t, "my-group", ConsumerGroup("my-group"))
}
