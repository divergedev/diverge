package secrets

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvResolver_Resolve_Success(t *testing.T) {
	r := &EnvResolver{}
	t.Setenv("TEST_SECRET_VAR", "secret-value")

	val, err := r.Resolve(context.Background(), SecretRef{Path: "TEST_SECRET_VAR"})
	require.NoError(t, err)
	assert.Equal(t, "secret-value", val)
}

func TestEnvResolver_Resolve_Unset(t *testing.T) {
	r := &EnvResolver{}
	require.NoError(t, os.Unsetenv("NONEXISTENT_SECRET_VAR_12345"))

	_, err := r.Resolve(context.Background(), SecretRef{Path: "NONEXISTENT_SECRET_VAR_12345"})
	assert.Error(t, err)
}
