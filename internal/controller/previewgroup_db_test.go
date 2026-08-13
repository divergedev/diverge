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
	"github.com/divergedev/diverge/internal/database"
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
	return nil, nil
}

func TestPreviewGroupReconciler_DB_Provision_InjectsEnvVars(t *testing.T) {
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

	// Second reconcile: creates child Environments and provisions DB
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-db-1"},
	})
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	if mockDB.provisionCalls != 1 {
		t.Errorf("expected 1 Provision call, got %d", mockDB.provisionCalls)
	}

	envName := childEnvironmentName("mr-db-1", "api")
	var childEnv divergeiov1alpha1.Environment
	if err := c.Get(context.Background(), types.NamespacedName{Name: envName, Namespace: "ns1"}, &childEnv); err != nil {
		t.Fatalf("failed to get child Environment: %v", err)
	}

	hasDBVar := false
	if childEnv.Spec.ServiceConfig != nil {
		for _, envVar := range childEnv.Spec.ServiceConfig.Env {
			if envVar.Name == "DB_URL" && envVar.Value == "postgres://user:pass@host/db" {
				hasDBVar = true
				break
			}
		}
	}
	if !hasDBVar {
		t.Errorf("child Environment missing injected DB_URL env var")
	}
}

func TestPreviewGroupReconciler_DB_Provision_Error_DoesNotBlock(t *testing.T) {
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

	// Second reconcile: creates child Environments and attempts DB provision
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "mr-db-2"},
	})
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	if mockDB.provisionCalls != 1 {
		t.Errorf("expected 1 Provision call, got %d", mockDB.provisionCalls)
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
				labelPreviewGroup:              "mr-db-3",
				"app.kubernetes.io/managed-by": "diverge",
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
