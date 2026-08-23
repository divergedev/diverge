package temporal

import (
	"os"
	"testing"

	"pgregory.net/rapid"
	"github.com/stretchr/testify/assert"
)

func TestTaskQueue(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		env      string
		expected string
		opts     []TaskQueueOption
	}{
		{
			name:     "empty env",
			base:     "my-queue",
			env:      "",
			expected: "my-queue",
		},
		{
			name:     "with env",
			base:     "my-queue",
			env:      "pr-123",
			expected: "my-queue-pr-123",
			opts:     nil,
		},
		{
			name:     "global with env",
			base:     "my-queue",
			env:      "pr-123",
			expected: "my-queue",
			opts:     []TaskQueueOption{Global()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DIVERGE_ENV", tt.env)
			assert.Equal(t, tt.expected, TaskQueue(tt.base, tt.opts...))
		})
	}
}

func TestTaskQueue_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		base := rapid.StringMatching(`^[a-z0-9-]+$`).Draw(t, "base")
		env := rapid.StringMatching(`^[a-z0-9-]+$`).Draw(t, "env")

		//nolint:errcheck
		os.Setenv("DIVERGE_ENV", env)
		defer os.Unsetenv("DIVERGE_ENV") //nolint:errcheck

		expected := base + "-" + env
		assert.Equal(t, expected, TaskQueue(base))
	})
}
