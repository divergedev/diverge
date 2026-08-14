package deployer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type mockDeployer struct {
	deployCalled   bool
	teardownCalled bool
	status         []ServiceStatus
	err            error
}

func (m *mockDeployer) Deploy(ctx context.Context, env *v1alpha1.Environment) error {
	m.deployCalled = true
	return m.err
}

func (m *mockDeployer) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	m.teardownCalled = true
	return m.err
}

func (m *mockDeployer) Status(ctx context.Context, env *v1alpha1.Environment) ([]ServiceStatus, error) {
	return m.status, m.err
}

func TestKEDADeployer_Deploy_WithCRD(t *testing.T) {
	inner := &mockDeployer{}
	c := fake.NewClientBuilder().Build()

	// Create the deployer
	d := &KEDADeployer{Inner: inner, Client: c}
	env := &v1alpha1.Environment{}
	env.Name = "test-env"
	env.Namespace = "test-ns"
	env.Spec.Deploy.Namespace = "same"

	err := d.Deploy(context.Background(), env)
	require.NoError(t, err)
	assert.True(t, inner.deployCalled)

	// Verify HSO was created
	hso := &unstructured.Unstructured{}
	hso.SetGroupVersionKind(hsoGVK)
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env", Namespace: "test-ns"}, hso)
	require.NoError(t, err)

	min, found, err := unstructured.NestedInt64(hso.Object, "spec", "replicas", "min")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, int64(0), min)

	max, found, err := unstructured.NestedInt64(hso.Object, "spec", "replicas", "max")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, int64(3), max)

	scaleTarget, found, err := unstructured.NestedString(hso.Object, "spec", "scaleTargetRef", "name")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "test-env", scaleTarget)
}

func TestKEDADeployer_Deploy_NoCRD(t *testing.T) {
	inner := &mockDeployer{}

	// Simulate NoMatchError when listing CRD
	c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "http.keda.sh", Kind: "HTTPScaledObject"}, SearchedVersions: []string{"v1alpha1"}}
		},
	}).Build()

	d := &KEDADeployer{Inner: inner, Client: c}
	env := &v1alpha1.Environment{}
	env.Name = "test-env"
	env.Namespace = "test-ns"

	err := d.Deploy(context.Background(), env)
	require.NoError(t, err, "should gracefully skip when CRD is missing")
	assert.True(t, inner.deployCalled)
}

func TestKEDADeployer_Teardown_NoCRD(t *testing.T) {
	inner := &mockDeployer{}

	c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "http.keda.sh", Kind: "HTTPScaledObject"}, SearchedVersions: []string{"v1alpha1"}}
		},
	}).Build()

	d := &KEDADeployer{Inner: inner, Client: c}
	env := &v1alpha1.Environment{}
	env.Name = "test-env"
	env.Namespace = "test-ns"

	err := d.Teardown(context.Background(), env)
	require.NoError(t, err, "should gracefully skip teardown when CRD is missing")
	assert.True(t, inner.teardownCalled)
}

func TestKEDADeployer_Status_WithHSO(t *testing.T) {
	inner := &mockDeployer{
		status: []ServiceStatus{
			{Name: "inner-service", Health: "Healthy"},
		},
	}

	hso := &unstructured.Unstructured{}
	hso.SetGroupVersionKind(hsoGVK)
	hso.SetName("test-env")
	hso.SetNamespace("test-ns")
	err := unstructured.SetNestedSlice(hso.Object, []interface{}{
		map[string]interface{}{
			"type":   "Ready",
			"status": "True",
		},
	}, "status", "conditions")
	require.NoError(t, err)

	c := fake.NewClientBuilder().WithRuntimeObjects(hso).Build()

	d := &KEDADeployer{Inner: inner, Client: c}
	env := &v1alpha1.Environment{}
	env.Name = "test-env"
	env.Namespace = "test-ns"
	env.Spec.Deploy.Namespace = "same"

	status, err := d.Status(context.Background(), env)
	require.NoError(t, err)
	require.Len(t, status, 2)
	assert.Equal(t, "Healthy", status[0].Health)
	assert.Equal(t, "http-scaled-object", status[1].Service)
	assert.Equal(t, "Healthy", status[1].Health)
}

func TestKEDADeployer_Status_NoHSO(t *testing.T) {
	inner := &mockDeployer{
		status: []ServiceStatus{
			{Name: "inner-service", Health: "Healthy"},
		},
	}

	c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "http.keda.sh", Kind: "HTTPScaledObject"}, SearchedVersions: []string{"v1alpha1"}}
		},
	}).Build()

	d := &KEDADeployer{Inner: inner, Client: c}
	env := &v1alpha1.Environment{}
	env.Name = "test-env"
	env.Namespace = "test-ns"

	status, err := d.Status(context.Background(), env)
	require.NoError(t, err)
	require.Len(t, status, 1)
	assert.Equal(t, "Healthy", status[0].Health)
}
