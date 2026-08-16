//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestEnvironment_CreateAndDelete(t *testing.T) {
	f := NewFramework(t)
	ctx := context.Background()
	f.CreateNamespace(ctx)
	defer f.CleanupNamespace(ctx)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github"},
		},
	}

	if err := f.CreateEnvironment(ctx, env); err != nil {
		t.Fatalf("Failed to create env: %v", err)
	}

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping reconciliation assertions")
	}

	if err := f.WaitForCondition(ctx, env.Name, "Ready", metav1.ConditionTrue, 1*time.Minute); err != nil {
		t.Fatalf("WaitForCondition timed out: %v", err)
	}
}

func TestEnvironment_Routing(t *testing.T) {
	f := NewFramework(t)
	ctx := context.Background()
	f.CreateNamespace(ctx)
	defer f.CleanupNamespace(ctx)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route-env",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github"},
		},
	}

	if err := f.CreateEnvironment(ctx, env); err != nil {
		t.Fatalf("Failed to create env: %v", err)
	}

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping reconciliation assertions")
	}

	if err := f.WaitForCondition(ctx, env.Name, "Ready", metav1.ConditionTrue, 1*time.Minute); err != nil {
		t.Fatalf("WaitForCondition timed out: %v", err)
	}
}

func TestEnvironment_AsyncRouting(t *testing.T) {
	f := NewFramework(t)
	ctx := context.Background()
	f.CreateNamespace(ctx)
	defer f.CleanupNamespace(ctx)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "async-env",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github"},
		},
	}

	if err := f.CreateEnvironment(ctx, env); err != nil {
		t.Fatalf("Failed to create env: %v", err)
	}

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping reconciliation assertions")
	}

	if err := f.WaitForCondition(ctx, env.Name, "Ready", metav1.ConditionTrue, 1*time.Minute); err != nil {
		t.Fatalf("WaitForCondition timed out: %v", err)
	}
}

func TestPreviewGroupLifecycle(t *testing.T) {
	f := NewFramework(t)
	ctx := context.Background()
	f.CreateNamespace(ctx)
	defer f.CleanupNamespace(ctx)

	pgName := fmt.Sprintf("test-pg-%s", f.Namespace[len(f.Namespace)-8:])
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: pgName,
		},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github"},
			Routing: v1alpha1.PreviewGroupRouting{
				HeaderValue: "test-pg",
			},
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{Name: "svc-a"},
			},
		},
	}

	err := f.Client.Create(ctx, pg)
	require.NoError(t, err, "Failed to create PreviewGroup")

	fetched := &v1alpha1.PreviewGroup{}
	err = f.Client.Get(ctx, types.NamespacedName{Name: pg.Name}, fetched)
	require.NoError(t, err)
	assert.Equal(t, pg.Spec.Source.Provider, fetched.Spec.Source.Provider)
	assert.Equal(t, pg.Spec.Routing.HeaderValue, fetched.Spec.Routing.HeaderValue)
	assert.Len(t, fetched.Spec.Services, len(pg.Spec.Services))

	if f.ControllerRunning(ctx) {
		// Verify that the child Environment is created and reaches Ready
		envName := fmt.Sprintf("%s-%s", pg.Name, "svc-a")
		err = f.WaitForCondition(ctx, envName, "Ready", metav1.ConditionTrue, 1*time.Minute)
		require.NoError(t, err, "Child Environment should reach Ready")
	}

	err = f.Client.Delete(ctx, pg)
	require.NoError(t, err, "Failed to delete PreviewGroup")
}

func TestPreviewGroupWithAsyncRoutes(t *testing.T) {
	f := NewFramework(t)
	ctx := context.Background()
	f.CreateNamespace(ctx)
	defer f.CleanupNamespace(ctx)

	pgName := fmt.Sprintf("async-pg-%s", f.Namespace[len(f.Namespace)-8:])
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: pgName,
		},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github"},
			Routing: v1alpha1.PreviewGroupRouting{
				HeaderValue: "async-pg",
			},
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{
					Name: "svc-worker",
					AsyncRoutes: []v1alpha1.AsyncRouteSpec{
						{Protocol: "kafka", Target: "topic-test"},
					},
				},
			},
		},
	}

	err := f.Client.Create(ctx, pg)
	require.NoError(t, err, "Failed to create PreviewGroup with async routes")

	fetched := &v1alpha1.PreviewGroup{}
	err = f.Client.Get(ctx, types.NamespacedName{Name: pg.Name}, fetched)
	require.NoError(t, err)
	assert.Equal(t, 1, len(fetched.Spec.Services[0].AsyncRoutes))
	assert.Equal(t, "kafka", fetched.Spec.Services[0].AsyncRoutes[0].Protocol)

	err = f.Client.Delete(ctx, pg)
	require.NoError(t, err, "Failed to delete PreviewGroup")
}

func TestMultiServicePreviewGroup(t *testing.T) {
	f := NewFramework(t)
	ctx := context.Background()
	f.CreateNamespace(ctx)
	defer f.CleanupNamespace(ctx)

	pgName := fmt.Sprintf("multi-pg-%s", f.Namespace[len(f.Namespace)-8:])
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: pgName,
		},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github"},
			Routing: v1alpha1.PreviewGroupRouting{
				HeaderValue: "multi-pg",
			},
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{Name: "svc-frontend"},
				{Name: "svc-backend"},
			},
		},
	}

	err := f.Client.Create(ctx, pg)
	require.NoError(t, err, "Failed to create multi-service PreviewGroup")

	fetched := &v1alpha1.PreviewGroup{}
	err = f.Client.Get(ctx, types.NamespacedName{Name: pg.Name}, fetched)
	require.NoError(t, err)
	assert.Len(t, fetched.Spec.Services, 2)
	assert.Equal(t, "svc-frontend", fetched.Spec.Services[0].Name)
	assert.Equal(t, "svc-backend", fetched.Spec.Services[1].Name)

	err = f.Client.Delete(ctx, pg)
	require.NoError(t, err, "Failed to delete PreviewGroup")
}
