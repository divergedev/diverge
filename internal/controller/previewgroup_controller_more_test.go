package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestPreviewGroupReconcile_Teardown(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-group",
			Finalizers:        []string{previewGroupFinalizer},
			DeletionTimestamp: &metav1.Time{Time: time.Now()},
		},
	}

	childEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pg-test-group-web-12345678",
			Namespace: "default",
			Labels: map[string]string{
				labelPreviewGroup: "test-group",
			},
		},
	}

	r, c := newTestPreviewGroupReconciler(pg, childEnv)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-group"}}

	// First reconcile starts deletion of child
	res, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, res.RequeueAfter > 0, "should requeue because child is not deleted immediately in fake client without explicit delete")

	// In the fake client, Delete without a finalizer removes the object immediately.
	// So we don't need to manually delete it, but let's verify it's gone or ignore NotFound.
	err = c.Delete(context.Background(), childEnv)
	if err != nil {
		assert.True(t, apierrors.IsNotFound(err), "Expected NotFound if already deleted")
	}

	// Second reconcile sees no children, removes finalizer
	res, err = r.Reconcile(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, res.Requeue)

	// Check finalizer is gone
	var updatedPg divergeiov1alpha1.PreviewGroup
	err = c.Get(context.Background(), types.NamespacedName{Name: "test-group"}, &updatedPg)
	assert.True(t, apierrors.IsNotFound(err), "PreviewGroup should be deleted once finalizer is removed")
}

func TestPreviewGroupReconcile_OrphanCleanup(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-group",
			Finalizers: []string{previewGroupFinalizer},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "web"},
			},
		},
	}

	// create orphan env
	orphanEnvName := childEnvironmentName("test-group", "api")
	orphanEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      orphanEnvName,
			Namespace: "default",
			Labels: map[string]string{
				labelPreviewGroup: "test-group",
			},
		},
	}

	r, c := newTestPreviewGroupReconciler(pg, orphanEnv)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-group"}}
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	// Ensure orphan is deleted
	var deletedEnv divergeiov1alpha1.Environment
	err = c.Get(context.Background(), types.NamespacedName{Name: orphanEnvName, Namespace: "default"}, &deletedEnv)
	assert.True(t, apierrors.IsNotFound(err), "orphan Environment should be deleted, got %v", err)
}

func TestPreviewGroupReconcile_UpdateChildOnImageChange(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-group",
			Finalizers: []string{previewGroupFinalizer},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "web", Image: "new-image"},
			},
		},
	}

	envName := childEnvironmentName("test-group", "web")
	childEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      envName,
			Namespace: "default",
			Labels: map[string]string{
				labelPreviewGroup: "test-group",
			},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{
				Image: "old-image",
			},
		},
	}

	r, c := newTestPreviewGroupReconciler(pg, childEnv)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-group"}}
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	var updatedEnv divergeiov1alpha1.Environment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: envName, Namespace: "default"}, &updatedEnv))
	assert.Equal(t, "new-image", updatedEnv.Spec.ServiceConfig.Image)
}

func TestPreviewGroupReconcile_ProtocolWiring(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-group",
			Finalizers: []string{previewGroupFinalizer},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "web", Protocol: "grpc"},
			},
		},
	}

	r, c := newTestPreviewGroupReconciler(pg)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-group"}}
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	envName := childEnvironmentName("test-group", "web")
	var childEnv divergeiov1alpha1.Environment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: envName, Namespace: "default"}, &childEnv))
	assert.Equal(t, "grpc", childEnv.Spec.ServiceConfig.Protocol)
}

func TestPreviewGroupReconcile_TTLExpiry(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-group",
			Finalizers: []string{previewGroupFinalizer},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Lifecycle: &divergeiov1alpha1.PreviewGroupLifecycle{
				TTL: &metav1.Duration{Duration: 1 * time.Hour},
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			CreatedAt: &metav1.Time{Time: time.Now().Add(-2 * time.Hour)}, // expired
		},
	}

	r, c := newTestPreviewGroupReconciler(pg)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-group"}}
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	var deletedPg divergeiov1alpha1.PreviewGroup
	err = c.Get(context.Background(), types.NamespacedName{Name: "test-group"}, &deletedPg)
	require.NoError(t, err)
	assert.False(t, deletedPg.DeletionTimestamp.IsZero(), "PreviewGroup should have DeletionTimestamp set due to TTL expiry")
}

func TestNeedsUpdate(t *testing.T) {
	r := &PreviewGroupReconciler{}

	tests := []struct {
		name     string
		existing *divergeiov1alpha1.Environment
		desired  *divergeiov1alpha1.Environment
		want     bool
	}{
		{
			name:     "image changed",
			existing: &divergeiov1alpha1.Environment{Spec: divergeiov1alpha1.EnvironmentSpec{ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{Image: "old"}}},
			desired:  &divergeiov1alpha1.Environment{Spec: divergeiov1alpha1.EnvironmentSpec{ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{Image: "new"}}},
			want:     true,
		},
		{
			name:     "header value changed",
			existing: &divergeiov1alpha1.Environment{Spec: divergeiov1alpha1.EnvironmentSpec{Routing: divergeiov1alpha1.EnvironmentRouting{HeaderValue: "old"}}},
			desired:  &divergeiov1alpha1.Environment{Spec: divergeiov1alpha1.EnvironmentSpec{Routing: divergeiov1alpha1.EnvironmentRouting{HeaderValue: "new"}}},
			want:     true,
		},
		{
			name:     "labels changed",
			existing: &divergeiov1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{labelPreviewGroup: "old"}}},
			desired:  &divergeiov1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{labelPreviewGroup: "new"}}},
			want:     true,
		},
		{
			name:     "port changed",
			existing: &divergeiov1alpha1.Environment{Spec: divergeiov1alpha1.EnvironmentSpec{ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{Port: 8080}}},
			desired:  &divergeiov1alpha1.Environment{Spec: divergeiov1alpha1.EnvironmentSpec{ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{Port: 8081}}},
			want:     true,
		},
		{
			name:     "protocol changed",
			existing: &divergeiov1alpha1.Environment{Spec: divergeiov1alpha1.EnvironmentSpec{ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{Protocol: "http"}}},
			desired:  &divergeiov1alpha1.Environment{Spec: divergeiov1alpha1.EnvironmentSpec{ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{Protocol: "grpc"}}},
			want:     true,
		},
		{
			name: "no change",
			existing: &divergeiov1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{labelPreviewGroup: "same"}},
				Spec: divergeiov1alpha1.EnvironmentSpec{
					ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{Image: "same", Port: 8080, Protocol: "http"},
					Routing:       divergeiov1alpha1.EnvironmentRouting{HeaderValue: "same"},
				},
			},
			desired: &divergeiov1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{labelPreviewGroup: "same"}},
				Spec: divergeiov1alpha1.EnvironmentSpec{
					ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{Image: "same", Port: 8080, Protocol: "http"},
					Routing:       divergeiov1alpha1.EnvironmentRouting{HeaderValue: "same"},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, r.needsUpdate(tt.existing, tt.desired))
		})
	}
}
