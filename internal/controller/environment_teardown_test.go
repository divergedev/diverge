package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestHandleTeardown_Success(t *testing.T) {
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

	res, err := r.handleTeardown(context.Background(), env)
	require.NoError(t, err)
	assert.Empty(t, res)

	assert.True(t, dep.teardownCalled)
	assert.True(t, rot.teardownCalled)
	assert.True(t, db.teardownCalled)

	updatedEnv := &divergeiov1alpha1.Environment{}
	err = client.Get(context.Background(), req.NamespacedName, updatedEnv)
	require.Error(t, err) // Should be deleted
}

func TestHandleTeardown_PartialFailure(t *testing.T) {
	now := metav1.Now()
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-env",
			Namespace:         "default",
			Finalizers:        []string{environmentFinalizer},
			DeletionTimestamp: &now,
		},
	}
	r, _, dep, rot, db := newTestReconciler(t, env, nil, "")

	dep.teardownErr = errors.New("deployer fails")
	rot.teardownErr = nil
	db.teardownErr = errors.New("db fails")

	res, err := r.handleTeardown(context.Background(), env)
	assert.Equal(t, 10*time.Second, res.RequeueAfter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deployer fails")
	assert.Contains(t, err.Error(), "db fails")

	assert.True(t, dep.teardownCalled)
	assert.True(t, rot.teardownCalled)
	assert.True(t, db.teardownCalled)
}

func TestHandleTeardown_AsyncTeardown(t *testing.T) {
	now := metav1.Now()
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-env",
			Namespace:         "default",
			Finalizers:        []string{environmentFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Routing: divergeiov1alpha1.EnvironmentRouting{
				AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{
					{Protocol: "sqs", Target: "test-queue"},
				},
			},
		},
	}
	r, _, _, _, _ := newTestReconciler(t, env, nil, "")
	fp := &fakeProvisioner{}
	r.AsyncProvisioner = fp

	res, err := r.handleTeardown(context.Background(), env)
	require.NoError(t, err)
	assert.Empty(t, res)

	assert.Equal(t, 1, fp.teardownCalls)
}
