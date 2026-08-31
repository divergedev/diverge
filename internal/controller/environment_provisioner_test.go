package controller

import (
	"context"
	"errors"
	"testing"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileProvisioning_DBFailure(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	r, _, _, _, db := newTestReconciler(t, env, nil, "")
	db.provisionErr = errors.New("db provision error")

	statusBase := env.DeepCopy()
	res, done, err := r.reconcileProvisioning(context.Background(), env, statusBase)
	assert.Empty(t, res)
	assert.True(t, done)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db provision error")
}

func TestReconcileProvisioning_RoutingFailure(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	r, _, _, rot, _ := newTestReconciler(t, env, nil, "")
	rot.reconcileErr = errors.New("routing error")

	statusBase := env.DeepCopy()
	res, done, err := r.reconcileProvisioning(context.Background(), env, statusBase)
	assert.Empty(t, res)
	assert.True(t, done)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "routing error")
}

func TestNotifyFailed_NilNotifier(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	r, _, _, _, _ := newTestReconciler(t, env, nil, "")

	// Should not panic when Notifier is nil
	assert.NotPanics(t, func() {
		r.notifyFailed(context.Background(), env, "test error")
	})
}

// A simple mock notifier to test the error path
type errorNotifier struct{}

func (e *errorNotifier) PostEnvironmentCreated(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	return nil
}
func (e *errorNotifier) PostEnvironmentReady(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	return nil
}
func (e *errorNotifier) PostEnvironmentFailed(ctx context.Context, env *divergeiov1alpha1.Environment, reason string) error {
	return errors.New("notify error")
}
func (e *errorNotifier) PostEnvironmentTeardown(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	return nil
}
func (e *errorNotifier) UpdateEnvironmentStatus(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	return nil
}

func TestNotifyFailed_ErrorHandled(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	r, _, _, _, _ := newTestReconciler(t, env, nil, "")
	r.Notifier = &errorNotifier{}

	assert.NotPanics(t, func() {
		r.notifyFailed(context.Background(), env, "test error")
	})
}

func TestCrossNamespaceSecretRef_Rejected(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			EnvFrom: []divergeiov1alpha1.SecretRef{
				{Namespace: "kube-system", Name: "secret-1"},
			},
		},
	}
	r, _, _, _, _ := newTestReconciler(t, env, nil, "")

	statusBase := env.DeepCopy()
	res, done, err := r.reconcileProvisioning(context.Background(), env, statusBase)
	assert.Empty(t, res)
	assert.True(t, done)
	require.NoError(t, err)

	cond := env.Status.Conditions[0]
	assert.Equal(t, "SecretRefValid", cond.Type)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "CrossNamespaceRef", cond.Reason)
}

func TestCrossNamespaceSecretRef_SameNamespaceAllowed(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			EnvFrom: []divergeiov1alpha1.SecretRef{
				{Namespace: "default", Name: "secret-1"},
				{Name: "secret-2"}, // implicit same namespace
			},
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				EnvFrom: []divergeiov1alpha1.SecretRef{
					{Namespace: "default", Name: "secret-3"},
				},
			},
		},
	}
	r, _, _, rot, db := newTestReconciler(t, env, nil, "")
	db.provisionErr = errors.New("stop here")
	rot.reconcileErr = errors.New("stop here")

	statusBase := env.DeepCopy()
	// This will fail later in reconcileProvisioning, but pass the SecretRef check
	_, _, _ = r.reconcileProvisioning(context.Background(), env, statusBase)

	for _, cond := range env.Status.Conditions {
		assert.NotEqual(t, "CrossNamespaceRef", cond.Reason)
	}
}
