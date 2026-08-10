package deployer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type mockFetcher struct {
	objects []unstructured.Unstructured
	err     error
}

func (m *mockFetcher) Fetch(ctx context.Context, env *v1alpha1.Environment) ([]unstructured.Unstructured, error) {
	return m.objects, m.err
}

func testEnv(name, namespace, nsMode string) *v1alpha1.Environment {
	return &v1alpha1.Environment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "diverge.io/v1alpha1",
			Kind:       "Environment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "test-uid-123",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Namespace: nsMode,
			},
		},
	}
}

func testDeploymentUnstructured(name, namespace string) unstructured.Unstructured {
	return unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"replicas": int64(1),
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{"app": name},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{"app": name},
					},
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  name,
								"image": "nginx:latest",
							},
						},
					},
				},
			},
		},
	}
}

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = appsv1.AddToScheme(s) // might already be in clientgoscheme
	return s
}

func ptr[T any](v T) *T {
	return &v
}

func TestDirectDeployer_Deploy_AppliesManifestsWithLabels(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()

	fetcher := &mockFetcher{
		objects: []unstructured.Unstructured{
			testDeploymentUnstructured("dep1", ""),
			testDeploymentUnstructured("dep2", ""),
		},
	}

	d := &DirectDeployer{
		Client:  c,
		Fetcher: fetcher,
	}

	env := testEnv("test-env", "test-ns", "same")
	err := d.Deploy(context.Background(), env)
	require.NoError(t, err)

	// Check that objects were applied and have correct labels
	var dep1 appsv1.Deployment
	err = c.Get(context.Background(), client.ObjectKey{Name: "dep1", Namespace: "test-ns"}, &dep1)
	require.NoError(t, err)
	assert.Equal(t, "test-env", dep1.Labels["diverge.io/environment"])
	assert.Equal(t, "diverge", dep1.Labels["diverge.io/managed-by"])

	var dep2 appsv1.Deployment
	err = c.Get(context.Background(), client.ObjectKey{Name: "dep2", Namespace: "test-ns"}, &dep2)
	require.NoError(t, err)
	assert.Equal(t, "test-env", dep2.Labels["diverge.io/environment"])
}

func TestDirectDeployer_Deploy_SetsOwnerReference(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()

	fetcher := &mockFetcher{
		objects: []unstructured.Unstructured{
			testDeploymentUnstructured("dep1", ""),
		},
	}

	d := &DirectDeployer{
		Client:  c,
		Fetcher: fetcher,
	}

	env := testEnv("test-env", "test-ns", "same")
	err := d.Deploy(context.Background(), env)
	require.NoError(t, err)

	var dep1 appsv1.Deployment
	err = c.Get(context.Background(), client.ObjectKey{Name: "dep1", Namespace: "test-ns"}, &dep1)
	require.NoError(t, err)

	require.Len(t, dep1.OwnerReferences, 1)
	assert.Equal(t, "test-env", dep1.OwnerReferences[0].Name)
	assert.Equal(t, "Environment", dep1.OwnerReferences[0].Kind)
	assert.Equal(t, "diverge.io/v1alpha1", dep1.OwnerReferences[0].APIVersion)
}

func TestDirectDeployer_Deploy_NoOwnerRef_CreateMode(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()

	fetcher := &mockFetcher{
		objects: []unstructured.Unstructured{
			testDeploymentUnstructured("dep1", ""),
		},
	}

	d := &DirectDeployer{
		Client:  c,
		Fetcher: fetcher,
	}

	env := testEnv("test-env", "test-ns", "create")
	err := d.Deploy(context.Background(), env)
	require.NoError(t, err)

	var dep1 appsv1.Deployment
	err = c.Get(context.Background(), client.ObjectKey{Name: "dep1", Namespace: env.PreviewNamespace()}, &dep1)
	require.NoError(t, err)

	assert.Len(t, dep1.OwnerReferences, 0)
}

func TestDirectDeployer_Deploy_EmptyManifests(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()

	fetcher := &mockFetcher{
		objects: []unstructured.Unstructured{},
	}

	d := &DirectDeployer{
		Client:  c,
		Fetcher: fetcher,
	}

	env := testEnv("test-env", "test-ns", "same")
	err := d.Deploy(context.Background(), env)
	require.NoError(t, err)
}

func TestDirectDeployer_Deploy_FetchError(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()

	fetcher := &mockFetcher{
		err: errors.New("fetch failed"),
	}

	d := &DirectDeployer{
		Client:  c,
		Fetcher: fetcher,
	}

	env := testEnv("test-env", "test-ns", "same")
	err := d.Deploy(context.Background(), env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch failed")
}

func TestDirectDeployer_Status_HealthyDeployment(t *testing.T) {
	s := testScheme()

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "test-ns",
			Labels: map[string]string{
				"diverge.io/environment": "test-env",
				"diverge.io/managed-by":  "diverge",
				"diverge.io/service":     "web-service",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(2)),
		},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 2,
			UpdatedReplicas:   2,
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(dep).WithStatusSubresource(&appsv1.Deployment{}).Build()

	d := &DirectDeployer{
		Client: c,
	}

	env := testEnv("test-env", "test-ns", "same")
	status, err := d.Status(context.Background(), env)
	require.NoError(t, err)

	require.Len(t, status, 1)
	assert.Equal(t, "web", status[0].Name)
	assert.Equal(t, "web-service", status[0].Service)
	assert.Equal(t, "Applied", status[0].SyncStatus)
	assert.Equal(t, "Healthy", status[0].Health)
}

func TestDirectDeployer_Status_ProgressingDeployment(t *testing.T) {
	s := testScheme()

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "test-ns",
			Labels: map[string]string{
				"diverge.io/environment": "test-env",
				"diverge.io/managed-by":  "diverge",
				"diverge.io/service":     "web-service",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(2)),
		},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 1,
			UpdatedReplicas:   1,
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(dep).WithStatusSubresource(&appsv1.Deployment{}).Build()

	d := &DirectDeployer{
		Client: c,
	}

	env := testEnv("test-env", "test-ns", "same")
	status, err := d.Status(context.Background(), env)
	require.NoError(t, err)

	require.Len(t, status, 1)
	assert.Equal(t, "Progressing", status[0].Health)
}

func TestDirectDeployer_Status_EmptyWhenNoResources(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()

	d := &DirectDeployer{
		Client: c,
	}

	env := testEnv("test-env", "test-ns", "same")
	status, err := d.Status(context.Background(), env)
	require.NoError(t, err)
	assert.Len(t, status, 0)
}

func TestDirectDeployer_Teardown_IsNoop(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()

	d := &DirectDeployer{
		Client: c,
	}

	env := testEnv("test-env", "test-ns", "same")
	err := d.Teardown(context.Background(), env)
	require.NoError(t, err)
}
