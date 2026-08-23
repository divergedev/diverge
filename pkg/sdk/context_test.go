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

func TestGetHeaderKey_Default(t *testing.T) {
	t.Setenv("DIVERGE_HEADER_KEY", "")
	assert.Equal(t, "x-diverge-env", GetHeaderKey())
}

func TestGetHeaderKey_EnvOverride(t *testing.T) {
	t.Setenv("DIVERGE_HEADER_KEY", "x-custom-env")
	assert.Equal(t, "x-custom-env", GetHeaderKey())
}
