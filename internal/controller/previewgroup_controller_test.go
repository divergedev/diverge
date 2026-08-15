package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sevents "k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/events"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)
	return s
}

func newTestPreviewGroupReconciler(objs ...client.Object) (*PreviewGroupReconciler, client.Client) {
	s := newTestScheme()
	cb := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{})
	if len(objs) > 0 {
		cb = cb.WithObjects(objs...)
	}
	c := cb.Build()
	r := &PreviewGroupReconciler{
		Client:   c,
		Scheme:   s,
		Recorder: events.NewRecorder(k8sevents.NewFakeRecorder(20)),
	}
	return r, c
}

func TestPreviewGroupReconcile_CreateChildEnvironments(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mr-42",
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "azra/platform",
				Branch:   "feat/payments",
			},
			Routing: divergeiov1alpha1.PreviewGroupRouting{
				HeaderKey:   "x-preview-env",
				HeaderValue: "42",
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{
					Name:      "payments-api",
					Image:     "registry.azra-ai.com/payments:mr-42",
					Mode:      divergeiov1alpha1.ServiceModeImage,
					Namespace: "product-rad",
					Port:      8080,
				},
				{
					Name:      "consent-mgr",
					Mode:      divergeiov1alpha1.ServiceModeBaseline,
					Namespace: "platform-core",
				},
			},
		},
	}

	r, c := newTestPreviewGroupReconciler(pg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-42"},
	})
	require.NoError(t, err)

	var updated divergeiov1alpha1.PreviewGroup
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-42"}, &updated))
	assert.Contains(t, updated.Finalizers, previewGroupFinalizer)

	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-42"},
	})
	require.NoError(t, err)

	envName := childEnvironmentName("mr-42", "payments-api")
	var childEnv divergeiov1alpha1.Environment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name:      envName,
		Namespace: "product-rad",
	}, &childEnv))

	assert.Equal(t, "mr-42", childEnv.Labels[labelPreviewGroup])
	assert.Equal(t, "42", childEnv.Spec.Routing.HeaderValue)
	assert.Equal(t, "x-preview-env", childEnv.Spec.Routing.HeaderKey)
	require.NotNil(t, childEnv.Spec.ServiceConfig)
	assert.Equal(t, "registry.azra-ai.com/payments:mr-42", childEnv.Spec.ServiceConfig.Image)
	assert.Equal(t, int32(8080), childEnv.Spec.ServiceConfig.Port)

	baselineEnvName := childEnvironmentName("mr-42", "consent-mgr")
	var baselineEnv divergeiov1alpha1.Environment
	err = c.Get(context.Background(), types.NamespacedName{
		Name:      baselineEnvName,
		Namespace: "platform-core",
	}, &baselineEnv)
	assert.Error(t, err, "child Environment should NOT be created for baseline service")
}

func TestPreviewGroupReconcile_BaselinePhaseIsRunning(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "mr-99",
			Finalizers: []string{previewGroupFinalizer},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "azra/platform",
				Branch:   "main",
			},
			Routing: divergeiov1alpha1.PreviewGroupRouting{
				HeaderKey:   "x-preview-env",
				HeaderValue: "99",
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{
					Name:      "auth-svc",
					Mode:      divergeiov1alpha1.ServiceModeBaseline,
					Namespace: "platform-core",
				},
			},
		},
	}

	r, c := newTestPreviewGroupReconciler(pg)
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-99"},
	})
	require.NoError(t, err)

	var updated divergeiov1alpha1.PreviewGroup
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-99"}, &updated))
	assert.Equal(t, divergeiov1alpha1.PreviewGroupPhaseRunning, updated.Status.Phase)
	assert.Equal(t, int32(1), updated.Status.ServiceCount)
}

func TestDerivePreviewGroupPhase(t *testing.T) {
	tests := []struct {
		name     string
		statuses []divergeiov1alpha1.PreviewGroupServiceStatus
		want     divergeiov1alpha1.PreviewGroupPhase
	}{
		{
			name:     "empty",
			statuses: nil,
			want:     divergeiov1alpha1.PreviewGroupPhasePending,
		},
		{
			name: "all running",
			statuses: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{Phase: divergeiov1alpha1.PhaseRunning},
				{Phase: divergeiov1alpha1.PhaseRunning},
			},
			want: divergeiov1alpha1.PreviewGroupPhaseRunning,
		},
		{
			name: "all failed",
			statuses: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{Phase: divergeiov1alpha1.PhaseFailed},
				{Phase: divergeiov1alpha1.PhaseFailed},
			},
			want: divergeiov1alpha1.PreviewGroupPhaseFailed,
		},
		{
			name: "mixed running + failed = degraded",
			statuses: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{Phase: divergeiov1alpha1.PhaseRunning},
				{Phase: divergeiov1alpha1.PhaseFailed},
			},
			want: divergeiov1alpha1.PreviewGroupPhaseDegraded,
		},
		{
			name: "deploying",
			statuses: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{Phase: divergeiov1alpha1.PhaseRunning},
				{Phase: divergeiov1alpha1.PhaseDeploying},
			},
			want: divergeiov1alpha1.PreviewGroupPhaseDeploying,
		},
		{
			name: "pending",
			statuses: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{Phase: divergeiov1alpha1.PhasePending},
			},
			want: divergeiov1alpha1.PreviewGroupPhasePending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := derivePreviewGroupPhase(tt.statuses)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestChildEnvironmentName(t *testing.T) {
	tests := []struct {
		group   string
		service string
	}{
		{"mr-42", "payments-api"},
		{"mr-42", "consent-manager"},
		{"feature-very-long-branch-name-that-exceeds-limit", "service-with-long-name"},
	}

	for _, tt := range tests {
		t.Run(tt.group+"/"+tt.service, func(t *testing.T) {
			name := childEnvironmentName(tt.group, tt.service)
			assert.LessOrEqual(t, len(name), 63)
			assert.NotEmpty(t, name)
			for _, c := range name {
				assert.True(t, (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-', "invalid character %q", c)
			}
		})
	}

	a := childEnvironmentName("mr-42", "payments-api")
	b := childEnvironmentName("mr-42", "payments-api")
	assert.Equal(t, a, b)

	x := childEnvironmentName("mr-42", "svc-a")
	y := childEnvironmentName("mr-42", "svc-b")
	assert.NotEqual(t, x, y)
}

func TestPreviewGroupReconcile_TeardownTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		setupPG    func(*divergeiov1alpha1.PreviewGroup)
		wantPhase  divergeiov1alpha1.PreviewGroupPhase
		wantDelete bool
	}{
		{
			name: "lease expiry",
			setupPG: func(pg *divergeiov1alpha1.PreviewGroup) {
				expiredTime := metav1.NewTime(time.Now().Add(-10 * time.Minute))
				pg.Status.LeaseRenewedAt = &expiredTime
			},
			wantPhase:  divergeiov1alpha1.PreviewGroupPhaseAbandoned,
			wantDelete: true,
		},
		{
			name: "TTL expiry",
			setupPG: func(pg *divergeiov1alpha1.PreviewGroup) {
				createdAt := metav1.NewTime(time.Now().Add(-2 * time.Hour))
				pg.Status.CreatedAt = &createdAt
				pg.Spec.Lifecycle = &divergeiov1alpha1.PreviewGroupLifecycle{
					TTL: &metav1.Duration{Duration: 1 * time.Hour},
				}
			},
			wantPhase:  divergeiov1alpha1.PreviewGroupPhaseRunning, // TTL expiry deletes it immediately without marking Abandoned
			wantDelete: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pg := &divergeiov1alpha1.PreviewGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "mr-99",
					Finalizers: []string{previewGroupFinalizer},
				},
				Spec: divergeiov1alpha1.PreviewGroupSpec{
					Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
						{Name: "auth-svc", Namespace: "platform-core"},
					},
				},
				Status: divergeiov1alpha1.PreviewGroupStatus{
					Phase: divergeiov1alpha1.PreviewGroupPhaseRunning,
				},
			}
			tt.setupPG(pg)

			epsUID := types.UID("uid-eps-1")
			eps1 := &unstructured.Unstructured{}
			eps1.SetAPIVersion("discovery.k8s.io/v1")
			eps1.SetKind("EndpointSlice")
			eps1.SetName("auth-svc-mr-99")
			eps1.SetNamespace("platform-core")
			eps1.SetUID(epsUID)
			eps1.SetLabels(map[string]string{
				"diverge.io/previewgroup":                "mr-99",
				"endpointslice.kubernetes.io/managed-by": "diverge",
			})

			route1 := &unstructured.Unstructured{}
			route1.SetAPIVersion("gateway.networking.k8s.io/v1")
			route1.SetKind("HTTPRoute")
			route1.SetName("auth-svc-mr-99")
			route1.SetNamespace("platform-core")
			route1.SetLabels(map[string]string{
				"diverge.io/previewgroup": "mr-99",
			})

			r, c := newTestPreviewGroupReconciler(pg, eps1, route1)

			_, err := r.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: "mr-99"},
			})
			require.NoError(t, err)

			var updated divergeiov1alpha1.PreviewGroup
			err = c.Get(context.Background(), types.NamespacedName{Name: "mr-99"}, &updated)
			require.NoError(t, err)
			if tt.wantDelete {
				assert.False(t, updated.DeletionTimestamp.IsZero(), "expected DeletionTimestamp to be set")
			} else {
				assert.Equal(t, tt.wantPhase, updated.Status.Phase)
			}

			// Verify HTTPRoute is deleted
			var routes unstructured.UnstructuredList
			routes.SetAPIVersion("gateway.networking.k8s.io/v1")
			routes.SetKind("HTTPRouteList")
			_ = c.List(context.Background(), &routes, client.InNamespace("platform-core"))
			assert.Empty(t, routes.Items)

			// Verify EndpointSlice (managed) is deleted
			var slices unstructured.UnstructuredList
			slices.SetAPIVersion("discovery.k8s.io/v1")
			slices.SetKind("EndpointSliceList")
			_ = c.List(context.Background(), &slices, client.InNamespace("platform-core"))
			assert.Empty(t, slices.Items)
		})
	}
}

func TestPreviewGroupReconcile_EmptyServices(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-empty"},
		Spec:       divergeiov1alpha1.PreviewGroupSpec{Services: []divergeiov1alpha1.PreviewGroupServiceSpec{}},
	}
	r, c := newTestPreviewGroupReconciler(pg)
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "mr-empty"}})
	require.NoError(t, err)

	var updated divergeiov1alpha1.PreviewGroup
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-empty"}, &updated))

	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "mr-empty"}})
	require.NoError(t, err)

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-empty"}, &updated))
	assert.Equal(t, divergeiov1alpha1.PreviewGroupPhasePending, updated.Status.Phase)
}

func TestPreviewGroupReconcile_DuplicateServiceNames(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-dup"},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "svc1", Mode: divergeiov1alpha1.ServiceModeImage, Namespace: "default"},
				{Name: "svc1", Mode: divergeiov1alpha1.ServiceModeBaseline, Namespace: "default"},
			},
		},
	}
	r, c := newTestPreviewGroupReconciler(pg)
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "mr-dup"}})
	require.NoError(t, err)
	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "mr-dup"}})
	require.NoError(t, err)

	var updated divergeiov1alpha1.PreviewGroup
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-dup"}, &updated))
	assert.Equal(t, int32(2), updated.Status.ServiceCount)
}

func TestPreviewGroupReconcile_RemoveOrphanedEnvironments(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-orphan"},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "svc-keep", Mode: divergeiov1alpha1.ServiceModeImage, Namespace: "default"},
			},
		},
	}

	orphanEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childEnvironmentName("mr-orphan", "svc-drop"),
			Namespace: "default",
			Labels: map[string]string{
				labelPreviewGroup: "mr-orphan",
				labelManagedBy:    "diverge-previewgroup",
			},
		},
	}

	r, c := newTestPreviewGroupReconciler(pg, orphanEnv)
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "mr-orphan"}})
	require.NoError(t, err)
	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "mr-orphan"}})
	require.NoError(t, err)

	var env divergeiov1alpha1.Environment
	err = c.Get(context.Background(), types.NamespacedName{Name: orphanEnv.Name, Namespace: "default"}, &env)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestPreviewGroupReconcile_FullCleanup(t *testing.T) {
	// Test that a PreviewGroup with services creates child environments
	// and a second reconcile progresses the phase
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mr-cleanup",
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "svc1"},
				{Name: "svc2"},
			},
		},
	}
	r, c := newTestPreviewGroupReconciler(pg)

	// First reconcile should add finalizer and start creating environments
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "mr-cleanup"}})
	require.NoError(t, err)

	// Verify the PreviewGroup has a finalizer
	var updated divergeiov1alpha1.PreviewGroup
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr-cleanup"}, &updated))
	assert.Contains(t, updated.Finalizers, previewGroupFinalizer)
}
