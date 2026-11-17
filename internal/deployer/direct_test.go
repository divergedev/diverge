package deployer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/multicluster"
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
			APIVersion: "divergedev.com/v1alpha1",
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
	assert.Equal(t, "test-env", dep1.Labels["divergedev.com/environment"])
	assert.Equal(t, "diverge", dep1.Labels["divergedev.com/managed-by"])

	var dep2 appsv1.Deployment
	err = c.Get(context.Background(), client.ObjectKey{Name: "dep2", Namespace: "test-ns"}, &dep2)
	require.NoError(t, err)
	assert.Equal(t, "test-env", dep2.Labels["divergedev.com/environment"])
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
	assert.Equal(t, "divergedev.com/v1alpha1", dep1.OwnerReferences[0].APIVersion)
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
			Name:       "web",
			Namespace:  "test-ns",
			Generation: 1,
			Labels: map[string]string{
				"divergedev.com/environment": "test-env",
				"divergedev.com/managed-by":  "diverge",
				"divergedev.com/service":     "web-service",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(2)),
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			AvailableReplicas:  2,
			UpdatedReplicas:    2,
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
				"divergedev.com/environment": "test-env",
				"divergedev.com/managed-by":  "diverge",
				"divergedev.com/service":     "web-service",
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

func TestDirectDeployer_Teardown_DeletesManagedWorkloads(t *testing.T) {
	for _, nsMode := range []string{"same", "create"} {
		t.Run(nsMode, func(t *testing.T) {
			s := testScheme()
			env := testEnv("test-env", "test-ns", nsMode)
			targetNS := "test-ns"
			if nsMode == "create" {
				targetNS = env.PreviewNamespace()
			}

			unmanagedDep := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "baseline-dep",
					Namespace: targetNS,
					Labels: map[string]string{
						"app": "baseline",
					},
				},
			}
			otherEnvSvc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-env-svc",
					Namespace: targetNS,
					Labels: map[string]string{
						"divergedev.com/environment": "other-env",
						"divergedev.com/managed-by":  "diverge",
					},
				},
			}
			otherNSJob := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-ns-job",
					Namespace: "unrelated-ns",
					Labels: map[string]string{
						"divergedev.com/environment": "test-env",
						"divergedev.com/managed-by":  "diverge",
					},
				},
			}

			c := fake.NewClientBuilder().WithScheme(s).WithObjects(unmanagedDep, otherEnvSvc, otherNSJob).Build()

			stsUnstructured := unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "StatefulSet",
					"metadata":   map[string]interface{}{"name": "sts1"},
					"spec":       map[string]interface{}{"serviceName": "svc1", "replicas": int64(1)},
				},
			}
			jobUnstructured := unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "batch/v1",
					"kind":       "Job",
					"metadata":   map[string]interface{}{"name": "job1"},
				},
			}
			svcUnstructured := unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Service",
					"metadata":   map[string]interface{}{"name": "svc1"},
					"spec":       map[string]interface{}{"ports": []interface{}{map[string]interface{}{"port": int64(8080)}}},
				},
			}

			d := &DirectDeployer{
				Client: c,
				Fetcher: &mockFetcher{
					objects: []unstructured.Unstructured{
						testDeploymentUnstructured("dep1", ""),
						stsUnstructured,
						jobUnstructured,
						svcUnstructured,
					},
				},
			}

			require.NoError(t, d.Deploy(context.Background(), env))

			statusBefore, err := d.Status(context.Background(), env)
			require.NoError(t, err)
			require.Len(t, statusBefore, 2)

			// Simulate a terminating preview Pod keeping the teardown Status gate closed
			now := metav1.Now()
			env.DeletionTimestamp = &now
			terminatingPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dep1-pod-abc",
					Namespace: targetNS,
					Labels: map[string]string{
						"divergedev.com/environment": "test-env",
					},
				},
				Status: corev1.PodStatus{Phase: corev1.PodRunning},
			}
			require.NoError(t, c.Create(context.Background(), terminatingPod))

			require.NoError(t, d.Teardown(context.Background(), env))

			// Managed Deployment, StatefulSet, Job, and Service are deleted
			assert.True(t, apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKey{Name: "dep1", Namespace: targetNS}, &appsv1.Deployment{})))
			assert.True(t, apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKey{Name: "sts1", Namespace: targetNS}, &appsv1.StatefulSet{})))
			assert.True(t, apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKey{Name: "job1", Namespace: targetNS}, &batchv1.Job{})))
			assert.True(t, apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKey{Name: "svc1", Namespace: targetNS}, &corev1.Service{})))

			// Unmanaged, other-environment, and other-namespace resources remain untouched
			require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "baseline-dep", Namespace: targetNS}, &appsv1.Deployment{}))
			require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "other-env-svc", Namespace: targetNS}, &corev1.Service{}))
			require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "other-ns-job", Namespace: "unrelated-ns"}, &batchv1.Job{}))

			// Status remains non-empty while terminatingPod is still running
			statusDraining, err := d.Status(context.Background(), env)
			require.NoError(t, err)
			require.Len(t, statusDraining, 1)
			assert.Equal(t, "Terminating", statusDraining[0].SyncStatus)

			// Once the pod terminates, Status becomes empty
			require.NoError(t, c.Delete(context.Background(), terminatingPod))
			statusAfter, err := d.Status(context.Background(), env)
			require.NoError(t, err)
			assert.Empty(t, statusAfter)
		})
	}
}

// CR2: Cross-namespace manifests should be rejected
func TestDirectDeployer_Deploy_RejectsCrossNamespace(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()

	// Create a manifest targeting a different namespace
	obj := testDeploymentUnstructured("dep1", "other-ns")

	fetcher := &mockFetcher{
		objects: []unstructured.Unstructured{obj},
	}

	d := &DirectDeployer{
		Client:  c,
		Fetcher: fetcher,
	}

	env := testEnv("test-env", "test-ns", "same")
	err := d.Deploy(context.Background(), env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cross-namespace manifests are not allowed")
}

// CR3: Unobserved generation should report Progressing, not Healthy
func TestDirectDeployer_Status_UnobservedGeneration(t *testing.T) {
	s := testScheme()

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "web",
			Namespace:  "test-ns",
			Generation: 2,
			Labels: map[string]string{
				"divergedev.com/environment": "test-env",
				"divergedev.com/managed-by":  "diverge",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1, // Behind Generation
			AvailableReplicas:  1,
			UpdatedReplicas:    1,
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(dep).WithStatusSubresource(&appsv1.Deployment{}).Build()

	d := &DirectDeployer{Client: c}
	env := testEnv("test-env", "test-ns", "same")
	status, err := d.Status(context.Background(), env)
	require.NoError(t, err)

	require.Len(t, status, 1)
	assert.Equal(t, "Progressing", status[0].Health, "should be Progressing when ObservedGeneration < Generation")
}

// CR3: Terminal rollout failure should report Degraded
func TestDirectDeployer_Status_TerminalFailure(t *testing.T) {
	s := testScheme()

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "web",
			Namespace:  "test-ns",
			Generation: 1,
			Labels: map[string]string{
				"divergedev.com/environment": "test-env",
				"divergedev.com/managed-by":  "diverge",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(2)),
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			AvailableReplicas:  0,
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentProgressing,
					Status: "False",
				},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(dep).WithStatusSubresource(&appsv1.Deployment{}).Build()

	d := &DirectDeployer{Client: c}
	env := testEnv("test-env", "test-ns", "same")
	status, err := d.Status(context.Background(), env)
	require.NoError(t, err)

	require.Len(t, status, 1)
	assert.Equal(t, "Degraded", status[0].Health, "should be Degraded when Progressing condition is False")
}

func TestDirectDeployer_Status_StatefulSetHealth(t *testing.T) {
	s := testScheme()

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "db",
			Namespace:  "test-ns",
			Generation: 1,
			Labels: map[string]string{
				"divergedev.com/environment": "test-env",
				"divergedev.com/managed-by":  "diverge",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr(int32(1)),
		},
		Status: appsv1.StatefulSetStatus{
			ObservedGeneration: 1,
			ReadyReplicas:      1,
			UpdatedReplicas:    1,
		},
	}

	stsProgressing := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "db-progressing",
			Namespace:  "test-ns",
			Generation: 2,
			Labels: map[string]string{
				"divergedev.com/environment": "test-env",
				"divergedev.com/managed-by":  "diverge",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr(int32(1)),
		},
		Status: appsv1.StatefulSetStatus{
			ObservedGeneration: 1,
		},
	}

	stsDegraded := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "db-degraded",
			Namespace:  "test-ns",
			Generation: 2,
			Labels: map[string]string{
				"divergedev.com/environment": "test-env",
				"divergedev.com/managed-by":  "diverge",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr(int32(1)),
		},
		Status: appsv1.StatefulSetStatus{
			ObservedGeneration: 2,
			ReadyReplicas:      0,
			UpdatedReplicas:    0,
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(sts, stsProgressing, stsDegraded).WithStatusSubresource(&appsv1.StatefulSet{}).Build()
	d := &DirectDeployer{Client: c}
	env := testEnv("test-env", "test-ns", "same")
	status, err := d.Status(context.Background(), env)
	require.NoError(t, err)

	require.Len(t, status, 3)

	statusMap := make(map[string]ServiceStatus)
	for _, st := range status {
		statusMap[st.Name] = st
	}

	assert.Equal(t, "Healthy", statusMap["db"].Health)
	assert.Equal(t, "Progressing", statusMap["db-progressing"].Health)
	assert.Equal(t, "Degraded", statusMap["db-degraded"].Health)
}

func TestDirectDeployer_Deploy_UpdatesExisting(t *testing.T) {
	s := testScheme()

	// Pre-existing deployment
	existingDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dep1",
			Namespace: "test-ns",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(existingDep).WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, client client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			return nil
		},
	}).Build()

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
}

func TestDirectDeployer_Deploy_RejectsClusterRole(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()

	obj := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRole",
			"metadata": map[string]interface{}{
				"name": "test-clusterrole",
			},
		},
	}
	fetcher := &mockFetcher{
		objects: []unstructured.Unstructured{obj},
	}
	d := &DirectDeployer{Client: c, Fetcher: fetcher}
	env := testEnv("test-env", "test-ns", "same")
	err := d.Deploy(context.Background(), env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not permitted")
}

func TestDirectDeployer_Deploy_RejectsNamespace(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()

	obj := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": "test-namespace",
			},
		},
	}
	fetcher := &mockFetcher{
		objects: []unstructured.Unstructured{obj},
	}
	d := &DirectDeployer{Client: c, Fetcher: fetcher}
	env := testEnv("test-env", "test-ns", "same")
	err := d.Deploy(context.Background(), env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not permitted")
}

func TestDirectDeployer_Deploy_AllowsDeployment(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()

	obj := testDeploymentUnstructured("dep1", "")
	fetcher := &mockFetcher{
		objects: []unstructured.Unstructured{obj},
	}
	d := &DirectDeployer{Client: c, Fetcher: fetcher}
	env := testEnv("test-env", "test-ns", "same")
	err := d.Deploy(context.Background(), env)
	require.NoError(t, err)
}

func TestDirectDeployer_Deploy_RejectsRBACAndEndpointKindsByDefault(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()
	env := testEnv("test-env", "test-ns", "same")

	disallowed := []struct {
		apiVersion string
		kind       string
	}{
		{"rbac.authorization.k8s.io/v1", "Role"},
		{"rbac.authorization.k8s.io/v1", "RoleBinding"},
		{"v1", "Endpoints"},
		{"discovery.k8s.io/v1", "EndpointSlice"},
	}

	for _, tc := range disallowed {
		obj := unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": tc.apiVersion,
				"kind":       tc.kind,
				"metadata": map[string]interface{}{
					"name": "test-disallowed",
				},
			},
		}
		d := NewDirectDeployer(c, &mockFetcher{objects: []unstructured.Unstructured{obj}})
		err := d.Deploy(context.Background(), env)
		require.Error(t, err, "expected kind %s to be rejected by default", tc.kind)
		assert.Contains(t, err.Error(), "not permitted")
	}
}

func TestDirectDeployer_WithAllowedKinds_CustomAllowlist(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()
	env := testEnv("test-env", "test-ns", "same")

	dep := testDeploymentUnstructured("dep1", "")
	d := NewDirectDeployer(c, &mockFetcher{objects: []unstructured.Unstructured{dep}}, WithAllowedKinds(map[string]bool{
		"Service": true,
	}))
	err := d.Deploy(context.Background(), env)
	require.Error(t, err, "Deployment should be rejected when custom allowlist only permits Service")

	// Empty map override must deny all kinds (not fall back to defaultAllowedKinds)
	d.WithAllowedKinds(map[string]bool{})
	err = d.Deploy(context.Background(), env)
	require.Error(t, err, "Deployment should be rejected when custom allowlist is empty")

	d.WithAllowedKinds(map[string]bool{"Deployment": true})
	err = d.Deploy(context.Background(), env)
	require.NoError(t, err)
}

func TestDirectDeployer_MultiClusterSpokeDeployStatusTeardown(t *testing.T) {
	ctx := context.Background()
	s := testScheme()
	hubClient := fake.NewClientBuilder().WithScheme(s).Build()
	spokeClient := fake.NewClientBuilder().WithScheme(s).Build()

	resolver := &multicluster.StaticResolver{
		Hub: &multicluster.Target{Name: "local", IsRemote: false, Client: hubClient},
		Spokes: map[string]*multicluster.Target{
			"us-west-burst": {Name: "us-west-burst", IsRemote: true, Client: spokeClient},
		},
	}

	dep := testDeploymentUnstructured("orders-preview", "")
	d := NewDirectDeployer(hubClient, &mockFetcher{objects: []unstructured.Unstructured{dep}}, WithClusterClients(resolver))

	env := testEnv("pr-federated", "team-orders", "same")
	env.Spec.Deploy.ClusterRef = &v1alpha1.ClusterTargetRef{
		Name: "us-west-burst",
		KubeconfigSecretRef: &v1alpha1.SecretRef{
			Name: "spoke-west-kubeconfig",
		},
	}

	require.NoError(t, d.Deploy(ctx, env))

	// Workload must exist on spoke WITHOUT cross-cluster OwnerReferences, and NOT on hub.
	var hubDep appsv1.Deployment
	err := hubClient.Get(ctx, client.ObjectKey{Name: "orders-preview", Namespace: "team-orders"}, &hubDep)
	assert.True(t, apierrors.IsNotFound(err))

	var spokeDep appsv1.Deployment
	require.NoError(t, spokeClient.Get(ctx, client.ObjectKey{Name: "orders-preview", Namespace: "team-orders"}, &spokeDep))
	assert.Empty(t, spokeDep.OwnerReferences, "cross-cluster OwnerReferences must not be set on spoke workloads")

	statuses, err := d.Status(ctx, env)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "orders-preview", statuses[0].Name)

	require.NoError(t, d.Teardown(ctx, env))
	err = spokeClient.Get(ctx, client.ObjectKey{Name: "orders-preview", Namespace: "team-orders"}, &spokeDep)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestDirectDeployer_Teardown_DeletesAllSupportedSpokeResourceKinds(t *testing.T) {
	ctx := context.Background()
	s := testScheme()
	hubClient := fake.NewClientBuilder().WithScheme(s).Build()
	spokeClient := fake.NewClientBuilder().WithScheme(s).Build()

	resolver := &multicluster.StaticResolver{
		Hub: &multicluster.Target{Name: "local", IsRemote: false, Client: hubClient},
		Spokes: map[string]*multicluster.Target{
			"spoke-west": {Name: "spoke-west", IsRemote: true, Client: spokeClient},
		},
	}

	manifests := []unstructured.Unstructured{
		{Object: map[string]interface{}{"apiVersion": "apps/v1", "kind": "DaemonSet", "metadata": map[string]interface{}{"name": "agent-ds"}}},
		{Object: map[string]interface{}{"apiVersion": "batch/v1", "kind": "CronJob", "metadata": map[string]interface{}{"name": "cleanup-cj"}}},
		{Object: map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "app-cm"}}},
		{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]interface{}{"name": "app-sec"}}},
		{Object: map[string]interface{}{"apiVersion": "networking.k8s.io/v1", "kind": "Ingress", "metadata": map[string]interface{}{"name": "app-ing"}}},
		{Object: map[string]interface{}{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]interface{}{"name": "app-sa"}}},
	}

	d := NewDirectDeployer(hubClient, &mockFetcher{objects: manifests}, WithClusterClients(resolver))
	env := testEnv("pr-all-kinds", "team-orders", "same")
	env.Spec.Deploy.ClusterRef = &v1alpha1.ClusterTargetRef{
		Name:                "spoke-west",
		KubeconfigSecretRef: &v1alpha1.SecretRef{Name: "spoke-west-kubeconfig"},
	}

	require.NoError(t, d.Deploy(ctx, env))
	require.NoError(t, spokeClient.Get(ctx, client.ObjectKey{Name: "agent-ds", Namespace: "team-orders"}, &appsv1.DaemonSet{}))
	require.NoError(t, spokeClient.Get(ctx, client.ObjectKey{Name: "cleanup-cj", Namespace: "team-orders"}, &batchv1.CronJob{}))
	require.NoError(t, spokeClient.Get(ctx, client.ObjectKey{Name: "app-cm", Namespace: "team-orders"}, &corev1.ConfigMap{}))
	require.NoError(t, spokeClient.Get(ctx, client.ObjectKey{Name: "app-sec", Namespace: "team-orders"}, &corev1.Secret{}))
	require.NoError(t, spokeClient.Get(ctx, client.ObjectKey{Name: "app-ing", Namespace: "team-orders"}, &networkingv1.Ingress{}))
	require.NoError(t, spokeClient.Get(ctx, client.ObjectKey{Name: "app-sa", Namespace: "team-orders"}, &corev1.ServiceAccount{}))

	require.NoError(t, d.Teardown(ctx, env))
	assert.True(t, apierrors.IsNotFound(spokeClient.Get(ctx, client.ObjectKey{Name: "agent-ds", Namespace: "team-orders"}, &appsv1.DaemonSet{})))
	assert.True(t, apierrors.IsNotFound(spokeClient.Get(ctx, client.ObjectKey{Name: "cleanup-cj", Namespace: "team-orders"}, &batchv1.CronJob{})))
	assert.True(t, apierrors.IsNotFound(spokeClient.Get(ctx, client.ObjectKey{Name: "app-cm", Namespace: "team-orders"}, &corev1.ConfigMap{})))
	assert.True(t, apierrors.IsNotFound(spokeClient.Get(ctx, client.ObjectKey{Name: "app-sec", Namespace: "team-orders"}, &corev1.Secret{})))
	assert.True(t, apierrors.IsNotFound(spokeClient.Get(ctx, client.ObjectKey{Name: "app-ing", Namespace: "team-orders"}, &networkingv1.Ingress{})))
	assert.True(t, apierrors.IsNotFound(spokeClient.Get(ctx, client.ObjectKey{Name: "app-sa", Namespace: "team-orders"}, &corev1.ServiceAccount{})))
}

func TestDirectDeployer_Teardown_LeastPrivilegeOptionalKindForbiddenAndExactSecretDeferral(t *testing.T) {
	ctx := context.Background()
	s := testScheme()

	// Simulate controller-runtime cached hub client (blocking/failing with cache sync deadline exceeded if
	// optional kinds like DaemonSet/CronJob/Ingress/PVC are listed!) AND least-privilege spoke RBAC (403 Forbidden).
	hubClient := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			switch list.(type) {
			case *appsv1.DaemonSetList,
				*appsv1.ReplicaSetList,
				*batchv1.CronJobList,
				*networkingv1.IngressList,
				*corev1.PersistentVolumeClaimList:
				return context.DeadlineExceeded
			default:
				return cl.List(ctx, list, opts...)
			}
		},
	}).Build()
	spokeClient := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			switch list.(type) {
			case *appsv1.DaemonSetList,
				*appsv1.ReplicaSetList,
				*batchv1.CronJobList,
				*networkingv1.IngressList,
				*corev1.PersistentVolumeClaimList:
				return apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "daemonsets"}, "", errors.New("forbidden by least-privilege RBAC"))
			default:
				return cl.List(ctx, list, opts...)
			}
		},
	}).Build()

	makeManifests := func() []unstructured.Unstructured {
		return []unstructured.Unstructured{
			testDeploymentUnstructured("echo-preview", ""),
			{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Service", "metadata": map[string]interface{}{"name": "echo-preview"}}},
			// User-supplied Secret sharing the diverge-db- prefix (must NOT be deferred during teardown).
			{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]interface{}{"name": "diverge-db-cache"}}},
		}
	}

	resolver := &multicluster.StaticResolver{
		Hub: &multicluster.Target{Name: "local", IsRemote: false, Client: hubClient},
		Spokes: map[string]*multicluster.Target{
			"spoke-lp": {Name: "spoke-lp", IsRemote: true, Client: spokeClient},
		},
	}

	// 1. Hub teardown never lists unauthorized optional kinds on the informer cache client.
	dHub := NewDirectDeployer(hubClient, &mockFetcher{objects: makeManifests()}, WithClusterClients(resolver))
	envHub := testEnv("istio-cleanup", "team-orders", "same")
	require.NoError(t, dHub.Deploy(ctx, envHub))
	delNow := metav1.Now()
	envHub.DeletionTimestamp = &delNow
	require.NoError(t, dHub.Teardown(ctx, envHub))
	assert.True(t, apierrors.IsNotFound(hubClient.Get(ctx, client.ObjectKey{Name: "echo-preview", Namespace: "team-orders"}, &appsv1.Deployment{})))
	assert.True(t, apierrors.IsNotFound(hubClient.Get(ctx, client.ObjectKey{Name: "echo-preview", Namespace: "team-orders"}, &corev1.Service{})))
	assert.True(t, apierrors.IsNotFound(hubClient.Get(ctx, client.ObjectKey{Name: "diverge-db-cache", Namespace: "team-orders"}, &corev1.Secret{})))

	// 2. Spoke teardown ignores 403 Forbidden on unused optional kinds and defers ONLY the exact controller DB Secret.
	d := NewDirectDeployer(hubClient, &mockFetcher{objects: makeManifests()}, WithClusterClients(resolver))
	env := testEnv("istio-cleanup-spoke", "team-orders", "same")
	env.Spec.Deploy.ClusterRef = &v1alpha1.ClusterTargetRef{
		Name:                "spoke-lp",
		KubeconfigSecretRef: &v1alpha1.SecretRef{Name: "spoke-lp-sec"},
	}
	require.NoError(t, d.Deploy(ctx, env))

	// Also seed the exact controller-generated DB env Secret, which MUST remain deferred until workload drain completes.
	generatedDBSecretName := controllerDBEnvSecretName(env.Name)
	genSec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generatedDBSecretName,
			Namespace: "team-orders",
			Labels: map[string]string{
				"divergedev.com/environment": env.Name,
				"divergedev.com/managed-by":  "diverge",
			},
		},
	}
	require.NoError(t, spokeClient.Create(ctx, genSec))

	env.DeletionTimestamp = &delNow
	require.NoError(t, d.Teardown(ctx, env))

	// Deployment, Service, and user-supplied diverge-db-cache Secret are deleted, while exact generated DB Secret is deferred.
	assert.True(t, apierrors.IsNotFound(spokeClient.Get(ctx, client.ObjectKey{Name: "echo-preview", Namespace: "team-orders"}, &appsv1.Deployment{})))
	assert.True(t, apierrors.IsNotFound(spokeClient.Get(ctx, client.ObjectKey{Name: "echo-preview", Namespace: "team-orders"}, &corev1.Service{})))
	assert.True(t, apierrors.IsNotFound(spokeClient.Get(ctx, client.ObjectKey{Name: "diverge-db-cache", Namespace: "team-orders"}, &corev1.Secret{})))
	require.NoError(t, spokeClient.Get(ctx, client.ObjectKey{Name: generatedDBSecretName, Namespace: "team-orders"}, &corev1.Secret{}))

	// 3. Unrelated pre-existing same-named Secret without matching ownership labels MUST NOT be deleted by named fallback.
	unmanagedSec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shared-baseline-sec",
			Namespace: "team-orders",
			Labels:    map[string]string{"app": "baseline"},
		},
	}
	require.NoError(t, hubClient.Create(ctx, unmanagedSec))
	dUnmanaged := NewDirectDeployer(hubClient, &mockFetcher{objects: []unstructured.Unstructured{
		{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]interface{}{"name": "shared-baseline-sec"}}},
	}}, WithClusterClients(resolver))
	require.NoError(t, dUnmanaged.Teardown(ctx, envHub))
	require.NoError(t, hubClient.Get(ctx, client.ObjectKey{Name: "shared-baseline-sec", Namespace: "team-orders"}, &corev1.Secret{}),
		"unmanaged baseline Secret must remain untouched when ownership labels do not match")

	// 4. Remote same-namespace manifest fetch failure must propagate as an error so the finalizer is retained.
	dFetchFail := NewDirectDeployer(hubClient, &mockFetcher{err: errors.New("manifest URL unreachable")}, WithClusterClients(resolver))
	errFetch := dFetchFail.Teardown(ctx, env)
	require.Error(t, errFetch)
	assert.Contains(t, errFetch.Error(), "failed to fetch manifests for remote teardown")
}
