package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestSharedProvider(t *testing.T) {
	provider := &SharedProvider{}
	env := &v1alpha1.Environment{}

	status, err := provider.Provision(context.Background(), env)
	require.NoError(t, err)
	assert.NotNil(t, status)

	err = provider.Teardown(context.Background(), env)
	require.NoError(t, err)
}

func TestSnapshotProvider(t *testing.T) {
	provider := &SnapshotProvider{}
	env := &v1alpha1.Environment{}

	status, err := provider.Provision(context.Background(), env)
	require.Error(t, err)
	assert.Nil(t, status)
}

func TestFreshProvider(t *testing.T) {
	provider := &FreshProvider{}
	env := &v1alpha1.Environment{}

	status, err := provider.Provision(context.Background(), env)
	require.Error(t, err)
	assert.Nil(t, status)
}
