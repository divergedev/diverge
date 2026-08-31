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
