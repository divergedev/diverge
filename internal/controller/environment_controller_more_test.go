package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sevents "k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/database"
	"github.com/divergedev/diverge/internal/deployer"
	"github.com/divergedev/diverge/internal/events"
)

type mockDeployer struct {
	deployErr      error
	teardownErr    error
	status         []deployer.ServiceStatus
	deployCalled   bool
	teardownCalled bool
}

func (m *mockDeployer) Deploy(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	m.deployCalled = true
	return m.deployErr
}
func (m *mockDeployer) Teardown(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	m.teardownCalled = true
	return m.teardownErr
}
func (m *mockDeployer) Status(ctx context.Context, env *divergeiov1alpha1.Environment) ([]deployer.ServiceStatus, error) {
	return m.status, nil
}

type mockRouter struct {
	reconcileErr    error
	teardownErr     error
	url             string
	reconcileCalled bool
	teardownCalled  bool
}

func (m *mockRouter) Reconcile(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	m.reconcileCalled = true
	return m.reconcileErr
}
func (m *mockRouter) Teardown(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	m.teardownCalled = true
	return m.teardownErr
}
func (m *mockRouter) GetExternalURL(env *divergeiov1alpha1.Environment) string {
	return m.url
}

type mockDB struct {
	provisionResult *database.DatabaseResult
	provisionErr    error
	teardownErr     error
	provisionCalled bool
	teardownCalled  bool
}

func (m *mockDB) Provision(ctx context.Context, env *divergeiov1alpha1.Environment) (*database.DatabaseResult, error) {
	m.provisionCalled = true
	return m.provisionResult, m.provisionErr
}
func (m *mockDB) Teardown(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	m.teardownCalled = true
	return m.teardownErr
}
func (m *mockDB) Status(ctx context.Context, env *divergeiov1alpha1.Environment) (*database.DatabaseStatus, error) {
	if m.provisionResult != nil {
		return &database.DatabaseStatus{Provisioned: m.provisionResult.Ready, Message: m.provisionResult.Message}, nil
	}
	return &database.DatabaseStatus{}, nil
}

func getTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func newTestReconciler(t *testing.T, env *divergeiov1alpha1.Environment, dbResult *database.DatabaseResult, url string) (*EnvironmentReconciler, client.Client, *mockDeployer, *mockRouter, *mockDB) {
	t.Helper()
	client := fake.NewClientBuilder().WithScheme(getTestScheme()).WithStatusSubresource(&divergeiov1alpha1.Environment{}).WithObjects(env).Build()

	dep := &mockDeployer{}
	rot := &mockRouter{url: url}
	db := &mockDB{provisionResult: dbResult}

	r := &EnvironmentReconciler{
		Client:           client,
		Scheme:           getTestScheme(),
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(10)),
		Deployer:         dep,
		Router:           rot,
		DatabaseProvider: db,
	}

	return r, client, dep, rot, db
}

func TestReconcile_SuccessfulProvision(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-env",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace: "create",
			},
		},
	}
	r, client, dep, rot, db := newTestReconciler(t, env, &database.DatabaseResult{Ready: true}, "https://test.com")

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-env", Namespace: "default"}}
	res, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)
	assert.Empty(t, res)

	assert.True(t, db.provisionCalled)
	assert.True(t, rot.reconcileCalled)
	assert.True(t, dep.deployCalled)

	updatedEnv := &divergeiov1alpha1.Environment{}
	require.NoError(t, client.Get(context.Background(), req.NamespacedName, updatedEnv))

	assert.Equal(t, divergeiov1alpha1.PhaseRunning, updatedEnv.Status.Phase)
	assert.Equal(t, "https://test.com", updatedEnv.Status.URL)
}

func TestReconcile_Teardown(t *testing.T) {
	now := metav1.Now()
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-env",
			Namespace:         "default",
			Finalizers:        []string{environmentFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace: "create",
			},
		},
	}
	r, client, dep, rot, db := newTestReconciler(t, env, nil, "")

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-env", Namespace: "default"}}
	res, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)
	assert.Empty(t, res)

	assert.True(t, dep.teardownCalled)
	assert.True(t, rot.teardownCalled)
	assert.True(t, db.teardownCalled)

	updatedEnv := &divergeiov1alpha1.Environment{}
	require.Error(t, client.Get(context.Background(), req.NamespacedName, updatedEnv)) // should be deleted
}

func TestReconcile_TTL(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-env",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Lifecycle: divergeiov1alpha1.EnvironmentLifecycle{
				TTL: &metav1.Duration{Duration: 1 * time.Hour},
			},
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace: "create",
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			CreatedAt: &metav1.Time{Time: time.Now().Add(-2 * time.Hour)}, // expired
		},
	}
	r, client, _, _, _ := newTestReconciler(t, env, &database.DatabaseResult{Ready: true}, "")

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-env", Namespace: "default"}}
	res, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)
	assert.Empty(t, res)

	updatedEnv := &divergeiov1alpha1.Environment{}
	require.NoError(t, client.Get(context.Background(), req.NamespacedName, updatedEnv))
	assert.NotNil(t, updatedEnv.DeletionTimestamp)
}
