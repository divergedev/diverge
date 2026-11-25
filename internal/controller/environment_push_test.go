package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/metrics"
	"github.com/divergedev/diverge/internal/previewgroup"
	"github.com/divergedev/diverge/pkg/database"
)

type commentingNotifier struct {
	successNotifier
	commentID int
	ready     []string
}

func (c *commentingNotifier) PostEnvironmentCreated(_ context.Context, env *divergeiov1alpha1.Environment) error {
	env.Status.CommentID = c.commentID
	return nil
}

func (c *commentingNotifier) PostEnvironmentReady(_ context.Context, env *divergeiov1alpha1.Environment) error {
	c.ready = append(c.ready, env.Status.CommitSHA)
	return nil
}

func pushedEnvironment(annotations map[string]string) *divergeiov1alpha1.Environment {
	return &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "mr-1",
			Namespace:         "default",
			Finalizers:        []string{environmentFinalizer},
			Annotations:       annotations,
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Hour)),
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{Provider: "gitlab", Project: "group/project", MR: 1, CommitSHA: "bbbbbbbbbbbb"},
			Deploy: divergeiov1alpha1.EnvironmentDeploy{Namespace: "create"},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase:     divergeiov1alpha1.PhaseRunning,
			CommitSHA: "aaaaaaaaaaaa",
		},
	}
}

func TestReconcile_FirstNoteIDIsKept(t *testing.T) {
	env := pushedEnvironment(nil)
	env.Finalizers = nil
	env.Status = divergeiov1alpha1.EnvironmentStatus{}
	r, c, _, _, _ := newTestReconciler(t, env, nil, "")
	r.Notifier = &commentingNotifier{commentID: 42}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: env.Name, Namespace: env.Namespace}}
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	got := &divergeiov1alpha1.Environment{}
	require.NoError(t, c.Get(context.Background(), req.NamespacedName, got))
	assert.Equal(t, 42, got.Status.CommentID)
}

func TestReconcile_PushRefreshesTheNoteAndWakesThePreview(t *testing.T) {
	env := pushedEnvironment(map[string]string{
		metrics.AnnotationHibernated:   "true",
		metrics.AnnotationHibernatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	})
	r, c, dep, _, _ := newTestReconciler(t, env, &database.DatabaseResult{Ready: true}, "https://preview.example.com")
	n := &commentingNotifier{}
	r.Notifier = n

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: env.Name, Namespace: env.Namespace}}
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, []string{"bbbbbbbbbbbb"}, n.ready)
	require.NotNil(t, dep.lastEnv)
	assert.NotContains(t, dep.lastEnv.Annotations, metrics.AnnotationHibernated)

	got := &divergeiov1alpha1.Environment{}
	require.NoError(t, c.Get(context.Background(), req.NamespacedName, got))
	assert.Equal(t, "bbbbbbbbbbbb", got.Status.CommitSHA)
	assert.NotContains(t, got.Annotations, metrics.AnnotationHibernated)
	assert.NotEmpty(t, got.Annotations[metrics.AnnotationLastTrafficAt])
	assert.NotEmpty(t, got.Annotations[metrics.AnnotationHibernatedSeconds])
}

func TestReconcile_SameCommitLeavesTheNoteAlone(t *testing.T) {
	env := pushedEnvironment(nil)
	env.Status.CommitSHA = env.Spec.Source.CommitSHA
	r, _, _, _, _ := newTestReconciler(t, env, &database.DatabaseResult{Ready: true}, "https://preview.example.com")
	n := &commentingNotifier{}
	r.Notifier = n

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: env.Name, Namespace: env.Namespace}}
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	assert.Empty(t, n.ready)
}

func TestRecordPushActivity_KeepsAGroupAttachedPreviewAsleep(t *testing.T) {
	env := pushedEnvironment(map[string]string{
		metrics.AnnotationHibernated:                "true",
		previewgroup.AnnotationAttachedPreviewGroup: "checkout",
	})
	r, c, _, _, _ := newTestReconciler(t, env, nil, "")

	require.NoError(t, r.recordPushActivity(context.Background(), env, time.Now()))

	got := &divergeiov1alpha1.Environment{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: env.Name, Namespace: env.Namespace}, got))
	assert.Equal(t, "true", got.Annotations[metrics.AnnotationHibernated])
	assert.NotEmpty(t, got.Annotations[metrics.AnnotationLastTrafficAt])
}
