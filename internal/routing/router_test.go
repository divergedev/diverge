package routing

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRouterInterfaceCompliance(t *testing.T) {
	ctx := context.Background()
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-42",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Routing: v1alpha1.EnvironmentRouting{
				Mode:       "subdomain",
				BaseDomain: "preview.example.com",
			},
		},
	}

	var noop Router = &NoopRouter{}
	require.NoError(t, noop.Reconcile(ctx, env))
	require.NoError(t, noop.Teardown(ctx, env))
	assert.Empty(t, noop.GetExternalURL(env))

	var gw Router = &GatewayRouter{}
	assert.Equal(t, "https://pr-42.preview.example.com", gw.GetExternalURL(env))

	var istio Router = &IstioRouter{}
	assert.Empty(t, istio.GetExternalURL(env))

	var composite Router = &CompositeRouter{
		Routers: map[string]Router{
			"noop":    noop,
			"gateway": gw,
		},
	}
	assert.Equal(t, "https://pr-42.preview.example.com", composite.GetExternalURL(env))
}
