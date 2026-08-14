package deployer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestKNativeDeployer_Deploy(t *testing.T) {
	ctx := context.Background()
	clientFake := fake.NewClientBuilder().Build()
	deployer := &KNativeDeployer{Client: clientFake}

	env := &v1alpha1.Environment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "diverge.io/v1alpha1",
			Kind:       "Environment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
			UID:       "1234",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Namespace: "same",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				Image: "my-image:latest",
			},
		},
	}

	err := deployer.Deploy(ctx, env)
	require.NoError(t, err)

	ksvc := &unstructured.Unstructured{}
	ksvc.SetGroupVersionKind(schema.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "Service"})
	err = clientFake.Get(ctx, client.ObjectKey{Name: "test-env", Namespace: "default"}, ksvc)
	require.NoError(t, err)

	// Test: ksvc has scale-to-zero annotation
	annos, found, err := unstructured.NestedStringMap(ksvc.Object, "spec", "template", "metadata", "annotations")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "0", annos["autoscaling.knative.dev/minScale"])

	// Test: ksvc has cluster-local visibility
	assert.Equal(t, "cluster-local", ksvc.GetAnnotations()["serving.knative.dev/visibility"])

	// Test: OwnerReferences set correctly
	owners := ksvc.GetOwnerReferences()
	require.Len(t, owners, 1)
	assert.Equal(t, "Environment", owners[0].Kind)
	assert.Equal(t, "test-env", owners[0].Name)

	// Test: Image correctly set
	containers, found, err := unstructured.NestedSlice(ksvc.Object, "spec", "template", "spec", "containers")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, containers, 1)
	container := containers[0].(map[string]interface{})
	assert.Equal(t, "my-image:latest", container["image"])
}

func TestKNativeDeployer_Teardown(t *testing.T) {
	ctx := context.Background()
	clientFake := fake.NewClientBuilder().Build()
	deployer := &KNativeDeployer{Client: clientFake}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}

	err := deployer.Teardown(ctx, env)
	require.NoError(t, err) // ignores NotFound
}

func TestKNativeDeployer_Status(t *testing.T) {
	ctx := context.Background()
	clientFake := fake.NewClientBuilder().Build()
	deployer := &KNativeDeployer{Client: clientFake}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}

	// Missing
	statuses, err := deployer.Status(ctx, env)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "Missing", statuses[0].Health)

	// Create ksvc
	ksvc := &unstructured.Unstructured{}
	ksvc.SetGroupVersionKind(schema.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "Service"})
	ksvc.SetName("test-env")
	ksvc.SetNamespace("default")

	require.NoError(t, unstructured.SetNestedSlice(ksvc.Object, []interface{}{
		map[string]interface{}{
			"type":   "Ready",
			"status": "True",
		},
	}, "status", "conditions"))

	require.NoError(t, unstructured.SetNestedField(ksvc.Object, "http://test-env.default.svc.cluster.local", "status", "url"))

	err = clientFake.Create(ctx, ksvc)
	require.NoError(t, err)

	// Healthy
	statuses, err = deployer.Status(ctx, env)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "Healthy", statuses[0].Health)
	assert.Equal(t, "http://test-env.default.svc.cluster.local", statuses[0].URL)
}
