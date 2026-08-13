package database

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoopDatabaseProvider(t *testing.T) {
	provider := &NoopDatabaseProvider{}
	ctx := context.Background()
	env := &v1alpha1.Environment{}

	res, err := provider.Provision(ctx, env)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Ready)

	err = provider.Teardown(ctx, env)
	assert.NoError(t, err)

	status, err := provider.Status(ctx, env)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.True(t, status.Provisioned)
}
