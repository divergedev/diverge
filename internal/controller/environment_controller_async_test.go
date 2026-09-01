package controller

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/async"
	"github.com/divergedev/diverge/pkg/database"
)

type fakeProvisionerDynamic struct {
	provisionFn func(ctx context.Context, env *divergeiov1alpha1.Environment, route divergeiov1alpha1.AsyncRouteSpec) (*async.ProvisionResult, error)
	teardownFn  func(ctx context.Context, env *divergeiov1alpha1.Environment, route divergeiov1alpha1.AsyncRouteSpec) error
}

func (f *fakeProvisionerDynamic) Name() string { return "fakedyn" }
func (f *fakeProvisionerDynamic) Provision(ctx context.Context, env *divergeiov1alpha1.Environment, route divergeiov1alpha1.AsyncRouteSpec) (*async.ProvisionResult, error) {
	if f.provisionFn != nil {
		return f.provisionFn(ctx, env, route)
	}
	return nil, nil
}
func (f *fakeProvisionerDynamic) Teardown(ctx context.Context, env *divergeiov1alpha1.Environment, route divergeiov1alpha1.AsyncRouteSpec) error {
	if f.teardownFn != nil {
		return f.teardownFn(ctx, env, route)
	}
	return nil
}

// Test 1: Full reconcile with async routes -> env vars on ServiceConfig
func TestAsyncRouting_FullReconcile(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-env",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{Namespace: "create"},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{
					{Protocol: "temporal", Target: "payments"},
				},
			},
		},
	}
	r, client, dep, _, _ := newTestReconciler(t, env.DeepCopy(), &database.DatabaseResult{Ready: true}, "https://test.com")

	fp := &fakeProvisioner{
		provisionResult: &async.ProvisionResult{
			EnvVars: map[string]string{"TEMPORAL_TASK_QUEUE": "payments-test-env"},
		},
	}
	r.AsyncProvisioner = fp

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-env", Namespace: "default"}}
	res, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)
	assert.Empty(t, res)

	updatedEnv := &divergeiov1alpha1.Environment{}
	require.NoError(t, client.Get(context.Background(), req.NamespacedName, updatedEnv))

	condition := meta.FindStatusCondition(updatedEnv.Status.Conditions, "AsyncRoutingReady")
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
	assert.Equal(t, "AsyncProvisioned", condition.Reason)

	require.NotNil(t, dep.lastEnv)
	require.NotNil(t, updatedEnv.Status.AsyncEnvVars)
	assert.Equal(t, "payments-test-env", updatedEnv.Status.AsyncEnvVars["TEMPORAL_TASK_QUEUE"])

	assert.True(t, dep.deployCalled)
	assert.Equal(t, divergeiov1alpha1.PhaseRunning, updatedEnv.Status.Phase)
}

// Test 2: Multiple async routes -> all env vars merged
func TestAsyncRouting_MultipleRoutes(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-env",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{Namespace: "create"},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{
					{Protocol: "temporal", Target: "payments"},
					{Protocol: "kafka", Target: "events"},
				},
			},
		},
	}
	r, client, _, _, _ := newTestReconciler(t, env.DeepCopy(), &database.DatabaseResult{Ready: true}, "https://test.com")

	var callCount atomic.Int32
	r.AsyncProvisioner = &fakeProvisionerDynamic{
		provisionFn: func(ctx context.Context, e *divergeiov1alpha1.Environment, route divergeiov1alpha1.AsyncRouteSpec) (*async.ProvisionResult, error) {
			callCount.Add(1)
			if route.Protocol == "temporal" {
				return &async.ProvisionResult{EnvVars: map[string]string{"TEMPORAL_TASK_QUEUE": "payments-test-env"}}, nil
			}
			return &async.ProvisionResult{EnvVars: map[string]string{"KAFKA_CONSUMER_GROUP": "events-test-env"}}, nil
		},
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-env", Namespace: "default"}}
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	updatedEnv := &divergeiov1alpha1.Environment{}
	require.NoError(t, client.Get(context.Background(), req.NamespacedName, updatedEnv))

	assert.Equal(t, int32(2), callCount.Load())
	condition := meta.FindStatusCondition(updatedEnv.Status.Conditions, "AsyncRoutingReady")
	require.NotNil(t, condition)
	assert.Contains(t, condition.Message, "2 async routes")

	envVars := updatedEnv.Status.AsyncEnvVars
	assert.Equal(t, "payments-test-env", envVars["TEMPORAL_TASK_QUEUE"])
	assert.Equal(t, "events-test-env", envVars["KAFKA_CONSUMER_GROUP"])
}

// Test 3: Async provision failure -> blocks deploy
func TestAsyncRouting_ProvisionFailure(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-env",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{Namespace: "create"},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "sqs", Target: "q"}},
			},
		},
	}
	r, client, dep, _, _ := newTestReconciler(t, env.DeepCopy(), &database.DatabaseResult{Ready: true}, "https://test.com")
	r.AsyncProvisioner = &fakeProvisioner{provisionErr: errors.New("boom")}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-env", Namespace: "default"}}
	_, err := r.Reconcile(context.Background(), req)
	require.Error(t, err)

	updatedEnv := &divergeiov1alpha1.Environment{}
	require.NoError(t, client.Get(context.Background(), req.NamespacedName, updatedEnv))

	condition := meta.FindStatusCondition(updatedEnv.Status.Conditions, "AsyncRoutingReady")
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.Equal(t, "AsyncProvisionFailed", condition.Reason)

	assert.False(t, dep.deployCalled)
	assert.Equal(t, divergeiov1alpha1.EnvironmentPhase(""), updatedEnv.Status.Phase)
}

// Test 4: Nil result from provisioner -> handled gracefully
func TestAsyncRouting_NilResult(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-env",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{Namespace: "create"},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "sqs", Target: "q"}},
			},
		},
	}
	r, client, dep, _, _ := newTestReconciler(t, env.DeepCopy(), &database.DatabaseResult{Ready: true}, "https://test.com")
	r.AsyncProvisioner = &fakeProvisioner{provisionResult: nil, provisionErr: nil}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-env", Namespace: "default"}}
	_, err := r.Reconcile(context.Background(), req)
	require.ErrorIs(t, err, async.ErrNilProvisionResult)

	updatedEnv := &divergeiov1alpha1.Environment{}
	require.NoError(t, client.Get(context.Background(), req.NamespacedName, updatedEnv))

	condition := meta.FindStatusCondition(updatedEnv.Status.Conditions, "AsyncRoutingReady")
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.False(t, dep.deployCalled)
}

// Test 5: Env var conflict between async routes
func TestAsyncRouting_Conflict(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-env",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{Namespace: "create"},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{
					{Protocol: "sqs", Target: "q1"},
					{Protocol: "sqs", Target: "q2"},
				},
			},
		},
	}
	r, client, _, _, _ := newTestReconciler(t, env.DeepCopy(), &database.DatabaseResult{Ready: true}, "https://test.com")

	r.AsyncProvisioner = &fakeProvisionerDynamic{
		provisionFn: func(ctx context.Context, e *divergeiov1alpha1.Environment, route divergeiov1alpha1.AsyncRouteSpec) (*async.ProvisionResult, error) {
			if route.Target == "q1" {
				return &async.ProvisionResult{EnvVars: map[string]string{"CONFLICT_VAR": "val1"}}, nil
			}
			return &async.ProvisionResult{EnvVars: map[string]string{"CONFLICT_VAR": "val2"}}, nil
		},
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-env", Namespace: "default"}}
	_, err := r.Reconcile(context.Background(), req)
	// M1: Terminal validation errors no longer requeue — error is nil
	require.NoError(t, err)

	updatedEnv := &divergeiov1alpha1.Environment{}
	require.NoError(t, client.Get(context.Background(), req.NamespacedName, updatedEnv))

	condition := meta.FindStatusCondition(updatedEnv.Status.Conditions, "AsyncRoutingReady")
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.Equal(t, "EnvVarConflict", condition.Reason)
}

// Test 6: No async routes -> backward compatible
func TestAsyncRouting_NoRoutes(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-env",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{Namespace: "create"},
		},
	}
	r, client, dep, _, _ := newTestReconciler(t, env.DeepCopy(), &database.DatabaseResult{Ready: true}, "https://test.com")
	r.AsyncProvisioner = &fakeProvisioner{}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-env", Namespace: "default"}}
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	updatedEnv := &divergeiov1alpha1.Environment{}
	require.NoError(t, client.Get(context.Background(), req.NamespacedName, updatedEnv))

	condition := meta.FindStatusCondition(updatedEnv.Status.Conditions, "AsyncRoutingReady")
	assert.Nil(t, condition) // no condition set

	assert.True(t, dep.deployCalled)
	assert.Equal(t, divergeiov1alpha1.PhaseRunning, updatedEnv.Status.Phase)
}

// Test 7: Teardown with async routes
func TestAsyncRouting_Teardown(t *testing.T) {
	now := metav1.Now()
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-env",
			Namespace:         "default",
			Finalizers:        []string{environmentFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{Namespace: "create"},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{
					{Protocol: "sqs", Target: "q1"},
					{Protocol: "sqs", Target: "q2"},
				},
			},
		},
	}
	r, _, _, _, _ := newTestReconciler(t, env.DeepCopy(), nil, "https://test.com")

	teardownCalls := 0
	r.AsyncProvisioner = &fakeProvisionerDynamic{
		teardownFn: func(ctx context.Context, e *divergeiov1alpha1.Environment, route divergeiov1alpha1.AsyncRouteSpec) error {
			teardownCalls++
			return nil
		},
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-env", Namespace: "default"}}
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, 2, teardownCalls)
}

// Test 8: AsyncProvisioner is nil -> skip silently
func TestAsyncRouting_NilProvisioner(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-env",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{Namespace: "create"},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{
					{Protocol: "sqs", Target: "q1"},
				},
			},
		},
	}
	r, client, dep, _, _ := newTestReconciler(t, env.DeepCopy(), &database.DatabaseResult{Ready: true}, "https://test.com")
	r.AsyncProvisioner = nil

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-env", Namespace: "default"}}
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	updatedEnv := &divergeiov1alpha1.Environment{}
	require.NoError(t, client.Get(context.Background(), req.NamespacedName, updatedEnv))

	condition := meta.FindStatusCondition(updatedEnv.Status.Conditions, "AsyncRoutingReady")
	assert.Nil(t, condition) // skipped silently

	assert.True(t, dep.deployCalled)
}
