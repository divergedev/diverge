package features

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
)

func TestUnleashProviderStub(t *testing.T) {
	// Verify registered
	assert.True(t, Providers.Has("unleash"))
	desc := Providers.Describe()
	assert.Contains(t, desc["unleash"], "Unleash")

	p, err := Providers.Create("unleash", registry.Deps{Logger: logr.Discard()})
	require.NoError(t, err)
	assert.Equal(t, "unleash", p.Type())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-unleash",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider: "unleash",
			},
		},
	}

	ctx := context.Background()

	// Provision
	res, err := p.Provision(ctx, env)
	require.NoError(t, err)
	assert.Equal(t, "unleash", res.ProviderType)
	assert.Equal(t, "diverge-pr-unleash", res.EnvVars["UNLEASH_APP_NAME"])
	assert.Equal(t, "pr-unleash", res.EnvVars["UNLEASH_ENVIRONMENT"])
	assert.Contains(t, res.Message, "268")

	// Status
	status, err := p.Status(ctx, env)
	require.NoError(t, err)
	assert.True(t, status.Ready)
	assert.Contains(t, status.Message, "268")

	// Teardown
	err = p.Teardown(ctx, env)
	require.NoError(t, err)
}
