package controller

import (
	"context"
	"testing"
	"time"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileLifecycle_TTLExpired(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Lifecycle: divergeiov1alpha1.EnvironmentLifecycle{
				TTL: &metav1.Duration{Duration: 1 * time.Hour},
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			CreatedAt: &metav1.Time{Time: time.Now().Add(-2 * time.Hour)}, // 2 hours ago, expired
		},
	}
	r, _, _, _, _ := newTestReconciler(t, env, nil, "")

	statusBase := env.DeepCopy()
	requeue, res, done, err := r.reconcileLifecycle(context.Background(), env, statusBase, divergeiov1alpha1.PhaseRunning)
	assert.True(t, done)
	require.NoError(t, err)
	assert.Empty(t, res)
	assert.Equal(t, time.Duration(0), requeue)
}

func TestReconcileLifecycle_TTLActive(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Lifecycle: divergeiov1alpha1.EnvironmentLifecycle{
				TTL: &metav1.Duration{Duration: 1 * time.Hour},
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			CreatedAt: &metav1.Time{Time: time.Now()}, // just created, active
		},
	}
	r, _, _, _, _ := newTestReconciler(t, env, nil, "")

	statusBase := env.DeepCopy()
	requeue, res, done, err := r.reconcileLifecycle(context.Background(), env, statusBase, divergeiov1alpha1.PhaseRunning)
	assert.False(t, done)
	require.NoError(t, err)
	assert.Empty(t, res)
	assert.Greater(t, requeue, time.Duration(0))
}

type successNotifier struct {
	notified bool
}

func (s *successNotifier) PostEnvironmentCreated(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	return nil
}
func (s *successNotifier) PostEnvironmentReady(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	s.notified = true
	return nil
}
func (s *successNotifier) PostEnvironmentFailed(ctx context.Context, env *divergeiov1alpha1.Environment, reason string) error {
	return nil
}
func (s *successNotifier) PostEnvironmentTeardown(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	return nil
}
func (s *successNotifier) UpdateEnvironmentStatus(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	return nil
}

func TestReconcileLifecycle_PhaseTransitionNotification(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase: divergeiov1alpha1.PhaseRunning, // new phase
		},
	}
	r, _, _, _, _ := newTestReconciler(t, env, nil, "")
	n := &successNotifier{}
	r.Notifier = n

	statusBase := env.DeepCopy()
	requeue, res, done, err := r.reconcileLifecycle(context.Background(), env, statusBase, divergeiov1alpha1.PhaseDeploying) // old phase
	assert.False(t, done)
	require.NoError(t, err)
	assert.Empty(t, res)
	assert.Equal(t, time.Duration(0), requeue)

	assert.True(t, n.notified)
}
