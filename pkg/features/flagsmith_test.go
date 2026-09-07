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

func TestFlagsmithProviderStub(t *testing.T) {
	// Verify registered
	assert.True(t, Providers.Has("flagsmith"))
	desc := Providers.Describe()
	assert.Contains(t, desc["flagsmith"], "Flagsmith")

	p, err := Providers.Create("flagsmith", registry.Deps{Logger: logr.Discard()})
	require.NoError(t, err)
	assert.Equal(t, "flagsmith", p.Type())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-flagsmith",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider: "flagsmith",
			},
		},
	}

	ctx := context.Background()

	// Provision
	res, err := p.Provision(ctx, env)
	require.NoError(t, err)
	assert.Equal(t, "flagsmith", res.ProviderType)
	assert.Equal(t, "preview-pr-flagsmith", res.EnvVars["FLAGSMITH_ENVIRONMENT_KEY"])
	assert.Contains(t, res.Message, "267")

	// Status
	status, err := p.Status(ctx, env)
	require.NoError(t, err)
	assert.True(t, status.Ready)
	assert.Contains(t, status.Message, "267")

	// Teardown
	err = p.Teardown(ctx, env)
	require.NoError(t, err)
}
