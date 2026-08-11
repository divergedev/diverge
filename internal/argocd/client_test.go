package argocd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestClient_ApplyApplication(t *testing.T) {
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(applicationGVK)
	app.SetName("test-app")
	app.SetNamespace("argocd")

	fc := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, client client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			return nil
		},
	}).Build()
	c := NewClient(fc, "argocd")

	err := c.ApplyApplication(context.Background(), app)
	require.NoError(t, err)

}

func TestClient_ApplyApplications(t *testing.T) {
	app1 := &unstructured.Unstructured{}
	app1.SetGroupVersionKind(applicationGVK)
	app1.SetName("app-1")
	app1.SetNamespace("argocd")

	app2 := &unstructured.Unstructured{}
	app2.SetGroupVersionKind(applicationGVK)
	app2.SetName("app-2")
	app2.SetNamespace("argocd")

	fc := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, client client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			return nil
		},
	}).Build()
	c := NewClient(fc, "argocd")

	err := c.ApplyApplications(context.Background(), []*unstructured.Unstructured{app1, app2})
	require.NoError(t, err)

}

func TestClient_DeleteApplication(t *testing.T) {
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(applicationGVK)
	app.SetName("test-app")
	app.SetNamespace("argocd")

	fc := fake.NewClientBuilder().WithObjects(app).Build()
	c := NewClient(fc, "argocd")

	err := c.DeleteApplication(context.Background(), "test-app")
	require.NoError(t, err)

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(applicationGVK)
	err = fc.Get(context.Background(), client.ObjectKey{Name: "test-app", Namespace: "argocd"}, fetched)
	require.Error(t, err)
}

func TestClient_DeleteApplicationsForEnvironment(t *testing.T) {
	app1 := &unstructured.Unstructured{}
	app1.SetGroupVersionKind(applicationGVK)
	app1.SetName("app-1")
	app1.SetNamespace("argocd")
	app1.SetLabels(map[string]string{
		"diverge.io/environment":           "env1",
		"diverge.io/environment-namespace": "ns1",
		"diverge.io/managed-by":            "diverge",
	})

	app2 := &unstructured.Unstructured{}
	app2.SetGroupVersionKind(applicationGVK)
	app2.SetName("app-2")
	app2.SetNamespace("argocd")
	app2.SetLabels(map[string]string{
		"diverge.io/environment":           "env2",
		"diverge.io/environment-namespace": "ns2",
		"diverge.io/managed-by":            "diverge",
	})

	fc := fake.NewClientBuilder().WithObjects(app1, app2).Build()
	c := NewClient(fc, "argocd")

	err := c.DeleteApplicationsForEnvironment(context.Background(), "env1", "ns1")
	require.NoError(t, err)

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(applicationGVK)
	err = fc.Get(context.Background(), client.ObjectKey{Name: "app-1", Namespace: "argocd"}, fetched)
	require.Error(t, err)

	err = fc.Get(context.Background(), client.ObjectKey{Name: "app-2", Namespace: "argocd"}, fetched)
	require.NoError(t, err)
}

func TestClient_GetSyncStatus(t *testing.T) {
	app1 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]interface{}{
				"name":      "app-1",
				"namespace": "argocd",
				"labels": map[string]interface{}{
					"diverge.io/environment":           "env1",
					"diverge.io/environment-namespace": "ns1",
					"diverge.io/managed-by":            "diverge",
					"diverge.io/service":               "svc1",
				},
			},
			"status": map[string]interface{}{
				"sync": map[string]interface{}{
					"status": "Synced",
				},
				"health": map[string]interface{}{
					"status": "Healthy",
				},
			},
		},
	}

	app2 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]interface{}{
				"name":      "app-2",
				"namespace": "argocd",
				"labels": map[string]interface{}{
					"diverge.io/environment":           "env1",
					"diverge.io/environment-namespace": "ns1",
					"diverge.io/managed-by":            "diverge",
					"diverge.io/service":               "svc2",
				},
			},
			// No status field to test Unknown
		},
	}

	fc := fake.NewClientBuilder().WithObjects(app1, app2).Build()
	c := NewClient(fc, "argocd")

	statuses, err := c.GetSyncStatus(context.Background(), "env1", "ns1")
	require.NoError(t, err)
	require.Len(t, statuses, 2)

	// Since they might not be ordered, let's create a map
	statusMap := make(map[string]ApplicationStatus)
	for _, s := range statuses {
		statusMap[s.Name] = s
	}

	assert.Equal(t, "Synced", statusMap["app-1"].SyncStatus)
	assert.Equal(t, "Healthy", statusMap["app-1"].Health)
	assert.Equal(t, "svc1", statusMap["app-1"].Service)

	assert.Equal(t, "Unknown", statusMap["app-2"].SyncStatus)
	assert.Equal(t, "Unknown", statusMap["app-2"].Health)
	assert.Equal(t, "svc2", statusMap["app-2"].Service)
}
