package database

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRegistryProvider struct{}

func (f *fakeRegistryProvider) Provision(_ context.Context, _ *v1alpha1.Environment) (*DatabaseResult, error) {
	return &DatabaseResult{Ready: true}, nil
}

func (f *fakeRegistryProvider) Teardown(_ context.Context, _ *v1alpha1.Environment) error {
	return nil
}

func (f *fakeRegistryProvider) Status(_ context.Context, _ *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{Provisioned: true}, nil
}

func TestProvidersRegistry(t *testing.T) {
	require.NotNil(t, Providers)

	if !Providers.Has("test-mock-provider") {
		Providers.Register("test-mock-provider", registry.Provider[DatabaseProvider]{
			Description: "Mock database provider for unit tests",
			Create: func(_ registry.Deps) (DatabaseProvider, error) {
				return &fakeRegistryProvider{}, nil
			},
		})
	}

	assert.True(t, Providers.Has("test-mock-provider"))
	p, err := Providers.Create("test-mock-provider", registry.Deps{})
	require.NoError(t, err)
	require.NotNil(t, p)

	res, err := p.Provision(context.Background(), &v1alpha1.Environment{})
	require.NoError(t, err)
	assert.True(t, res.Ready)
}
