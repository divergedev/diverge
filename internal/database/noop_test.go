package database

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func TestNoopDatabaseProvider(t *testing.T) {
	provider := &NoopDatabaseProvider{}
	ctx := context.Background()
	env := &v1alpha1.Environment{}

	res, err := provider.Provision(ctx, env)
	assert.NoError(t, err)
	assert.True(t, res.Ready)

	err = provider.Teardown(ctx, env)
	assert.NoError(t, err)

	status, err := provider.Status(ctx, env)
	assert.NoError(t, err)
	assert.True(t, status.Provisioned)
}
