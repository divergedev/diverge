package controller

import (
	"context"
	"errors"
	"testing"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileDeploy_Success(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	r, _, dep, _, _ := newTestReconciler(t, env, nil, "")
	dep.deployErr = nil

	statusBase := env.DeepCopy()
	res, done, err := r.reconcileDeploy(context.Background(), env, statusBase)
	assert.Empty(t, res)
	assert.False(t, done)
	require.NoError(t, err)

	cond := meta.FindStatusCondition(env.Status.Conditions, "ServicesReady")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
}

func TestReconcileDeploy_Failure(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	r, _, dep, _, _ := newTestReconciler(t, env, nil, "")
	dep.deployErr = errors.New("deploy failure")

	statusBase := env.DeepCopy()
	res, done, err := r.reconcileDeploy(context.Background(), env, statusBase)
	assert.Empty(t, res)
	assert.True(t, done)
	require.Error(t, err)

	cond := meta.FindStatusCondition(env.Status.Conditions, "ServicesReady")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "DeployFailed", cond.Reason)
}

func TestReconcileDeploy_NilDeployer(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	r, _, _, _, _ := newTestReconciler(t, env, nil, "")
	r.Deployer = nil

	statusBase := env.DeepCopy()
	res, done, err := r.reconcileDeploy(context.Background(), env, statusBase)
	assert.Empty(t, res)
	assert.False(t, done)
	require.NoError(t, err)

	cond := meta.FindStatusCondition(env.Status.Conditions, "ServicesReady")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
}
