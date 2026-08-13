package routing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestGatewayRouter_Reconcile_GRPCRoute(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"web"},
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				Protocol: "grpc",
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1alpha2")
	u.SetKind("GRPCRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web", Namespace: "default"}, u)
	require.NoError(t, err, "GRPCRoute should exist")

	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	require.Len(t, rules, 1)

	// Verify HTTPRoute does not exist
	uHttp := &unstructured.Unstructured{}
	uHttp.SetAPIVersion("gateway.networking.k8s.io/v1")
	uHttp.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web", Namespace: "default"}, uHttp)
	assert.Error(t, err, "HTTPRoute should not exist")
}

func TestGatewayRouter_Reconcile_GAMMAMeshRoute(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"web"},
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "web-svc",
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	// Check ingress route
	uIngress := &unstructured.Unstructured{}
	uIngress.SetAPIVersion("gateway.networking.k8s.io/v1")
	uIngress.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web", Namespace: "default"}, uIngress)
	require.NoError(t, err, "Ingress route should exist")

	// Check mesh route
	uMesh := &unstructured.Unstructured{}
	uMesh.SetAPIVersion("gateway.networking.k8s.io/v1")
	uMesh.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web-mesh", Namespace: "default"}, uMesh)
	require.NoError(t, err, "Mesh route should exist")

	parents, _, _ := unstructured.NestedSlice(uMesh.Object, "spec", "parentRefs")
	require.Len(t, parents, 1)
	assert.Equal(t, "web-svc", parents[0].(map[string]interface{})["name"])
	assert.Equal(t, "Service", parents[0].(map[string]interface{})["kind"])
}

func TestGatewayRouter_Reconcile_GRPCWithGAMMA(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"api"},
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				Protocol:    "grpc",
				ServiceName: "api-svc",
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	// Check mesh route
	uMesh := &unstructured.Unstructured{}
	uMesh.SetAPIVersion("gateway.networking.k8s.io/v1alpha2")
	uMesh.SetKind("GRPCRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-api-mesh", Namespace: "default"}, uMesh)
	require.NoError(t, err, "Mesh GRPCRoute should exist")

	parents, _, _ := unstructured.NestedSlice(uMesh.Object, "spec", "parentRefs")
	require.Len(t, parents, 1)
	assert.Equal(t, "api-svc", parents[0].(map[string]interface{})["name"])
	assert.Equal(t, "Service", parents[0].(map[string]interface{})["kind"])
}

func TestGatewayRouter_Teardown_GRPCRoutes(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1alpha2")
	u.SetKind("GRPCRoute")
	u.SetName("test-env-web")
	u.SetNamespace("default")
	u.SetLabels(map[string]string{
		"diverge.io/environment": "test-env",
		"diverge.io/managed-by":  "diverge",
	})
	require.NoError(t, c.Create(context.Background(), u))

	err := r.Teardown(context.Background(), env)
	require.NoError(t, err)

	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web", Namespace: "default"}, u)
	assert.Error(t, err, "GRPCRoute should be deleted")
}
