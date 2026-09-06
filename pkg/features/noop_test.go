package features

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestNoopProvider(t *testing.T) {
	p := &NoopProvider{}
	assert.Equal(t, "noop", p.Type())

	ctx := context.Background()
	env := &v1alpha1.Environment{}

	res, err := p.Provision(ctx, env)
	require.NoError(t, err)
	assert.Equal(t, "noop", res.ProviderType)
	assert.Equal(t, "Feature flag management disabled (noop)", res.Message)

	err = p.Teardown(ctx, env)
	assert.NoError(t, err)

	status, err := p.Status(ctx, env)
	require.NoError(t, err)
	assert.True(t, status.Ready)
}
