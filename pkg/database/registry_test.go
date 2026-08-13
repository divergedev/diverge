package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/database"
)

// stubProvider is a minimal DatabaseProvider for testing.
type stubProvider struct {
	name string
}

func (s *stubProvider) Provision(_ context.Context, _ *v1alpha1.Environment) (*database.DatabaseResult, error) {
	return &database.DatabaseResult{Ready: true, Message: s.name}, nil
}

func (s *stubProvider) Teardown(_ context.Context, _ *v1alpha1.Environment) error {
	return nil
}

func (s *stubProvider) Status(_ context.Context, _ *v1alpha1.Environment) (*database.DatabaseStatus, error) {
	return &database.DatabaseStatus{Provisioned: true, Message: s.name}, nil
}

func TestRegisterAndGetProvider(t *testing.T) {
	database.ResetRegistry()
	defer database.ResetRegistry()

	factory := func(cfg database.ProviderConfig) (database.DatabaseProvider, error) {
		return &stubProvider{name: "test-" + cfg.AdminDSN}, nil
	}

	database.RegisterProvider("test-provider", factory)

	got, ok := database.GetProvider("test-provider")
	require.True(t, ok, "provider should be registered")
	require.NotNil(t, got)

	provider, err := got(database.ProviderConfig{AdminDSN: "abc"})
	require.NoError(t, err)

	result, err := provider.Provision(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, "test-abc", result.Message)
}

func TestGetProvider_NotFound(t *testing.T) {
	database.ResetRegistry()
	defer database.ResetRegistry()

	_, ok := database.GetProvider("nonexistent")
	assert.False(t, ok)
}

func TestRegisterProvider_Duplicate_Panics(t *testing.T) {
	database.ResetRegistry()
	defer database.ResetRegistry()

	factory := func(cfg database.ProviderConfig) (database.DatabaseProvider, error) {
		return &stubProvider{}, nil
	}

	database.RegisterProvider("dup", factory)

	assert.Panics(t, func() {
		database.RegisterProvider("dup", factory)
	}, "registering same name twice should panic")
}

func TestRegisteredProviders(t *testing.T) {
	database.ResetRegistry()
	defer database.ResetRegistry()

	factory := func(cfg database.ProviderConfig) (database.DatabaseProvider, error) {
		return &stubProvider{}, nil
	}

	database.RegisterProvider("alpha", factory)
	database.RegisterProvider("beta", factory)

	names := database.RegisteredProviders()
	assert.Len(t, names, 2)
	assert.Contains(t, names, "alpha")
	assert.Contains(t, names, "beta")
}

func TestResetRegistry(t *testing.T) {
	database.ResetRegistry()

	factory := func(cfg database.ProviderConfig) (database.DatabaseProvider, error) {
		return &stubProvider{}, nil
	}
	database.RegisterProvider("will-be-gone", factory)

	database.ResetRegistry()

	_, ok := database.GetProvider("will-be-gone")
	assert.False(t, ok, "provider should be gone after reset")
	assert.Empty(t, database.RegisteredProviders())
}
