package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/database"
)

type mockDatabaseProvider struct {
	provisionResult *database.DatabaseResult
	provisionErr    error
	teardownErr     error
	provisionCalls  int
	teardownCalls   int
}

func (m *mockDatabaseProvider) Provision(ctx context.Context, env *divergeiov1alpha1.Environment) (*database.DatabaseResult, error) {
	m.provisionCalls++
	return m.provisionResult, m.provisionErr
}

func (m *mockDatabaseProvider) Teardown(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	m.teardownCalls++
	return m.teardownErr
}

func (m *mockDatabaseProvider) Status(ctx context.Context, env *divergeiov1alpha1.Environment) (*database.DatabaseStatus, error) {
	return &database.DatabaseStatus{}, nil
}

// TestPreviewGroupReconciler_DB_DelegatedToChildEnvironment verifies that
// PreviewGroup does NOT call DatabaseProvider.Provision directly — it sets
// the Database spec on child Environment CRs and lets EnvironmentReconciler
// handle provisioning.
func TestPreviewGroupReconciler_DB_DelegatedToChildEnvironment(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mr-db-1",
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Database: &divergeiov1alpha1.EnvironmentDatabase{
				Mode: "schema",
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{
					Name:      "api",
					Image:     "api:latest",
					Namespace: "ns1",
				},
			},
		},
	}

	mockDB := &mockDatabaseProvider{
		provisionResult: &database.DatabaseResult{
			EnvVars: map[string]string{
				"DB_URL": "postgres://user:pass@host/db",
			},
		},
	}

	r, c := newTestPreviewGroupReconciler(pg)
	r.DatabaseProvider = mockDB

	// First reconcile: adds finalizer
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-db-1"},
	})
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}

	// Second reconcile: creates child Environments (but does NOT provision DB)
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-db-1"},
	})
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	// H5: PreviewGroup should NOT call Provision — EnvironmentReconciler does that
	if mockDB.provisionCalls != 0 {
		t.Errorf("expected 0 Provision calls (delegated to EnvironmentReconciler), got %d", mockDB.provisionCalls)
	}

	// Verify child Environment was created with the Database spec set
	envName := childEnvironmentName("mr-db-1", "api")
	var childEnv divergeiov1alpha1.Environment
	if err := c.Get(context.Background(), types.NamespacedName{Name: envName, Namespace: "ns1"}, &childEnv); err != nil {
		t.Fatalf("failed to get child Environment: %v", err)
	}

	// Child should have the Database spec so EnvironmentReconciler provisions it
	if childEnv.Spec.Database.Mode != "schema" {
		t.Errorf("expected child Database.Mode='schema', got %q", childEnv.Spec.Database.Mode)
	}
}

// TestPreviewGroupReconciler_DB_NotProvisionedByPreviewGroup verifies that
// PreviewGroup does not provision DB even when DatabaseProvider returns errors
// (because it should not be calling Provision at all).
func TestPreviewGroupReconciler_DB_NotProvisionedByPreviewGroup(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mr-db-2",
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Database: &divergeiov1alpha1.EnvironmentDatabase{
				Mode: "schema",
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{
					Name:      "api",
					Image:     "api:latest",
					Namespace: "ns1",
				},
			},
		},
	}

	mockDB := &mockDatabaseProvider{
		provisionErr: errors.New("provision error"),
	}

	r, c := newTestPreviewGroupReconciler(pg)
	r.DatabaseProvider = mockDB

	// First reconcile: adds finalizer
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-db-2"},
	})
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}

	// Second reconcile: creates child Environments (no DB provision)
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-db-2"},
	})
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	// H5: PreviewGroup should NOT call Provision
	if mockDB.provisionCalls != 0 {
		t.Errorf("expected 0 Provision calls, got %d", mockDB.provisionCalls)
	}

	envName := childEnvironmentName("mr-db-2", "api")
	var childEnv divergeiov1alpha1.Environment
	if err := c.Get(context.Background(), types.NamespacedName{Name: envName, Namespace: "ns1"}, &childEnv); err != nil {
		t.Fatalf("failed to get child Environment: %v", err)
	}
}

func TestPreviewGroupReconciler_DB_Teardown_CalledOnDelete(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "mr-db-3",
			DeletionTimestamp: &metav1.Time{Time: time.Now()},
			Finalizers:        []string{previewGroupFinalizer},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Database: &divergeiov1alpha1.EnvironmentDatabase{
				Mode: "schema",
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{
					Name:      "api",
					Image:     "api:latest",
					Namespace: "ns1",
				},
			},
		},
	}

	envName := childEnvironmentName("mr-db-3", "api")
	childEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      envName,
			Namespace: "ns1",
			Labels: map[string]string{
				labelPreviewGroup: "mr-db-3",
				labelManagedBy:    "diverge-previewgroup",
			},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Mode: "schema",
			},
		},
	}

	mockDB := &mockDatabaseProvider{}

	r, _ := newTestPreviewGroupReconciler(pg, childEnv)
	r.DatabaseProvider = mockDB

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-db-3"},
	})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if mockDB.teardownCalls != 1 {
		t.Errorf("expected 1 Teardown call, got %d", mockDB.teardownCalls)
	}
}

func TestPreviewGroupReconciler_DB_NilProvider_NoOp(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mr-db-4",
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Database: &divergeiov1alpha1.EnvironmentDatabase{
				Mode: "schema",
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{
					Name:      "api",
					Image:     "api:latest",
					Namespace: "ns1",
				},
			},
		},
	}

	r, c := newTestPreviewGroupReconciler(pg)
	r.DatabaseProvider = nil

	// First reconcile: adds finalizer
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-db-4"},
	})
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}

	// Second reconcile: creates child Environments
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-db-4"},
	})
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	envName := childEnvironmentName("mr-db-4", "api")
	var childEnv divergeiov1alpha1.Environment
	if err := c.Get(context.Background(), types.NamespacedName{Name: envName, Namespace: "ns1"}, &childEnv); err != nil {
		t.Fatalf("failed to get child Environment: %v", err)
	}
}
