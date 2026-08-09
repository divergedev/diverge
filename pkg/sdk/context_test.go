package sdk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithEnvironment(t *testing.T) {
	ctx := context.Background()
	envName := "pr-123"

	ctx = WithEnvironment(ctx, envName)
	val := EnvironmentFromContext(ctx)
	assert.Equal(t, envName, val)
}

func TestFromContextEmpty(t *testing.T) {
	ctx := context.Background()
	val := EnvironmentFromContext(ctx)
	assert.Equal(t, "", val)
}
