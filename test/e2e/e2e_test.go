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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Scenario C: Teardown cleanup
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
			Source: v1alpha1.EnvironmentSource{Provider: "github", Project: "divergedev/test-app", Branch: "feat/test-branch"},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err, "Failed to create env")

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping reconciliation assertions")
	}

	err = f.WaitForCondition(ctx, env.Name, "ServicesReady", metav1.ConditionTrue, defaultTimeout)
	require.NoError(t, err, "WaitForCondition ServicesReady timed out")

	// Delete Environment
	err = f.Client.Delete(ctx, env)
	require.NoError(t, err, "Failed to delete Environment")

	err = f.WaitForEnvironmentDeleted(ctx, env.Name, defaultTimeout)
	require.NoError(t, err, "Environment did not delete in time")

	// Assert no orphaned Deployments
	var deps appsv1.DeploymentList
	err = f.Client.List(ctx, &deps, client.InNamespace(f.Namespace))
	require.NoError(t, err)
	assert.Len(t, deps.Items, 0, "Expected no deployments remaining")

	// Assert no orphaned Services
	var svcs corev1.ServiceList
	err = f.Client.List(ctx, &svcs, client.InNamespace(f.Namespace))
	require.NoError(t, err)
	assert.Len(t, svcs.Items, 0, "Expected no services remaining")

	// Assert no orphaned HTTPRoutes
	var routes gatewayv1.HTTPRouteList
	err = f.Client.List(ctx, &routes, client.InNamespace(f.Namespace))
	require.NoError(t, err)
	assert.Len(t, routes.Items, 0, "Expected no HTTPRoutes remaining")
}

// Scenario B: Async routes provisioning
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
			Source: v1alpha1.EnvironmentSource{Provider: "github", Project: "divergedev/test-app", Branch: "feat/test-branch"},
			Routing: v1alpha1.EnvironmentRouting{
				AsyncRoutes: []v1alpha1.AsyncRouteSpec{
					{Protocol: "kafka", Target: "topic-test"},
					{Protocol: "temporal", Target: "task-queue-test"},
				},
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err, "Failed to create env")

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping reconciliation assertions")
	}

	err = f.WaitForCondition(ctx, env.Name, "ServicesReady", metav1.ConditionTrue, defaultTimeout)
	require.NoError(t, err, "WaitForCondition timed out")

	// Assert AsyncEnvVars populated
	var fetched v1alpha1.Environment
	err = f.Client.Get(ctx, types.NamespacedName{Name: env.Name, Namespace: f.Namespace}, &fetched)
	require.NoError(t, err)

	assert.NotEmpty(t, fetched.Status.AsyncEnvVars, "AsyncEnvVars should be populated")

	// Assert async route status conditions
	hasAsyncReady := false
	for _, cond := range fetched.Status.Conditions {
		if cond.Type == "AsyncRoutingReady" && cond.Status == metav1.ConditionTrue {
			hasAsyncReady = true
			break
		}
	}
	assert.True(t, hasAsyncReady, "AsyncRoutingReady condition should be true")
}

// Scenario A: PreviewGroup → Environment lifecycle
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
			Source: v1alpha1.EnvironmentSource{Provider: "github", Project: "divergedev/test-app", Branch: "feat/test-branch"},
			Routing: v1alpha1.PreviewGroupRouting{
				HeaderValue: "test-pg",
			},
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{Name: "svc-a", Namespace: f.Namespace},
				{Name: "svc-b", Namespace: f.Namespace},
			},
		},
	}

	err := f.Client.Create(ctx, pg)
	require.NoError(t, err, "Failed to create PreviewGroup")

	fetched := &v1alpha1.PreviewGroup{}
	err = f.Client.Get(ctx, types.NamespacedName{Name: pg.Name}, fetched)
	require.NoError(t, err)
	assert.Len(t, fetched.Spec.Services, 2)

	if f.ControllerRunning(ctx) {
		// Assert child Environments are created and transition to ServicesReady
		envNameA := fmt.Sprintf("%s-%s", pg.Name, "svc-a")
		envNameB := fmt.Sprintf("%s-%s", pg.Name, "svc-b")

		err = f.WaitForCondition(ctx, envNameA, "ServicesReady", metav1.ConditionTrue, defaultTimeout)
		require.NoError(t, err, "Child Environment svc-a should reach ServicesReady")

		err = f.WaitForCondition(ctx, envNameB, "ServicesReady", metav1.ConditionTrue, defaultTimeout)
		require.NoError(t, err, "Child Environment svc-b should reach ServicesReady")

		// Delete PreviewGroup
		err = f.Client.Delete(ctx, pg)
		require.NoError(t, err, "Failed to delete PreviewGroup")

		// Assert child Environments are cleaned up
		err = f.WaitForEnvironmentDeleted(ctx, envNameA, defaultTimeout)
		require.NoError(t, err, "Child Environment svc-a not deleted")

		err = f.WaitForEnvironmentDeleted(ctx, envNameB, defaultTimeout)
		require.NoError(t, err, "Child Environment svc-b not deleted")
	} else {
		_ = f.Client.Delete(ctx, pg)
		t.Skip("controller not deployed — skipping reconciliation assertions")
	}
}

// Scenario D: Multi-service routing
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
			Source: v1alpha1.EnvironmentSource{Provider: "github", Project: "divergedev/test-app", Branch: "feat/test-branch"},
			Routing: v1alpha1.PreviewGroupRouting{
				HeaderValue: "multi-pg",
			},
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{Name: "svc-frontend", Namespace: f.Namespace},
				{Name: "svc-backend", Namespace: f.Namespace},
				{Name: "svc-worker", Namespace: f.Namespace},
			},
		},
	}

	err := f.Client.Create(ctx, pg)
	require.NoError(t, err, "Failed to create multi-service PreviewGroup")

	fetched := &v1alpha1.PreviewGroup{}
	err = f.Client.Get(ctx, types.NamespacedName{Name: pg.Name}, fetched)
	require.NoError(t, err)
	assert.Len(t, fetched.Spec.Services, 3)

	if f.ControllerRunning(ctx) {
		// Assert each gets HTTPRoute and check routing header propagation
		for _, svc := range pg.Spec.Services {
			envName := fmt.Sprintf("%s-%s", pg.Name, svc.Name)
			err = f.WaitForCondition(ctx, envName, "ServicesReady", metav1.ConditionTrue, defaultTimeout)
			require.NoError(t, err, fmt.Sprintf("Child Environment %s should reach ServicesReady", svc.Name))

			// Assert each service gets its own HTTPRoute
			var route gatewayv1.HTTPRoute
			routeName := fmt.Sprintf("%s-%s", envName, svc.Name)
			err = wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, defaultTimeout, true, func(ctx context.Context) (bool, error) {
				getErr := f.Client.Get(ctx, types.NamespacedName{Name: routeName, Namespace: f.Namespace}, &route)
				if getErr != nil {
					if apierrors.IsNotFound(getErr) {
						return false, nil
					}
					return false, getErr
				}
				return true, nil
			})
			require.NoError(t, err, "Expected HTTPRoute for service "+svc.Name)

			// Assert the route was created with rules
			assert.NotEmpty(t, route.Spec.Rules, "HTTPRoute should have rules")
		}

		err = f.Client.Delete(ctx, pg)
		require.NoError(t, err, "Failed to delete PreviewGroup")
	} else {
		_ = f.Client.Delete(ctx, pg)
	}
}

// Scenario E: AgentTask CRD creation and OpenAPI schema defaults
func TestAgentTaskCRDValidation(t *testing.T) {
	f := NewFramework(t)
	ctx := context.Background()
	f.CreateNamespace(ctx)
	defer f.CleanupNamespace(ctx)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-agent-task",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Verify AgentTask CRD schema in Kind",
			Repository: v1alpha1.AgentTaskRepository{
				URL: "https://github.com/divergedev/diverge",
			},
			BudgetUSD: "10.00",
		},
	}

	err := f.Client.Create(ctx, task)
	require.NoError(t, err, "Failed to create AgentTask")

	fetched := &v1alpha1.AgentTask{}
	err = f.Client.Get(ctx, types.NamespacedName{Name: task.Name, Namespace: f.Namespace}, fetched)
	require.NoError(t, err)
	assert.Equal(t, "Verify AgentTask CRD schema in Kind", fetched.Spec.Objective)
	assert.Equal(t, "main", fetched.Spec.Repository.BaseBranch, "CRD default should set BaseBranch=main")
	assert.Equal(t, int32(5), fetched.Spec.MaxIterations, "CRD default should set MaxIterations=5")
	assert.True(t, fetched.Spec.DraftPR, "CRD default should set DraftPR=true")

	err = f.Client.Delete(ctx, fetched)
	require.NoError(t, err, "Failed to delete AgentTask")
}

