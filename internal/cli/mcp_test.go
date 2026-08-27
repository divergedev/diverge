package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPascalToSnake(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"GetEnvironment", "get_environment"},
		{"CreatePreviewGroup", "create_preview_group"},
		{"WatchEnvironments", "watch_environments"},
		{"StreamLogs", "stream_logs"},
		{"ABCTest", "a_b_c_test"}, // Basic check, though usually we don't have this
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, pascalToSnake(tt.input))
		})
	}
}

func TestDivergeToolNamer(t *testing.T) {
	assert.Equal(t, "diverge_get_environment", divergeToolNamer("EnvironmentService", "GetEnvironment"))
}
