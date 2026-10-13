package features

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
)

func TestRegistry_BuiltinProviders(t *testing.T) {
	assert.True(t, Providers.Has("noop"))
	assert.True(t, Providers.Has("none"))
	assert.True(t, Providers.Has("configmap"))
	assert.True(t, Providers.Has("flipt"))
	assert.True(t, Providers.Has("flagsmith"))
	assert.True(t, Providers.Has("unleash"))

	list := Providers.List()
	assert.Contains(t, list, "noop")
	assert.Contains(t, list, "none")
	assert.Contains(t, list, "configmap")
	assert.Contains(t, list, "flipt")
	assert.Contains(t, list, "flagsmith")
	assert.Contains(t, list, "unleash")

	desc := Providers.Describe()
	assert.NotEmpty(t, desc["configmap"])
	assert.NotEmpty(t, desc["noop"])
	assert.NotEmpty(t, desc["flipt"])
	assert.NotEmpty(t, desc["flagsmith"])
	assert.NotEmpty(t, desc["unleash"])

	p, err := Providers.Create("noop", registry.Deps{})
	require.NoError(t, err)
	assert.Equal(t, "noop", p.Type())

	ctx := context.Background()
	env := &v1alpha1.Environment{}

	status, err := p.Status(ctx, env)
	require.NoError(t, err)
	assert.True(t, status.Ready)

	res, err := p.Provision(ctx, env)
	require.NoError(t, err)
	assert.Equal(t, "noop", res.ProviderType)

	err = p.Teardown(ctx, env)
	assert.NoError(t, err)
}
