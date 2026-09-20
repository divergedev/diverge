package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
	_ "github.com/divergedev/diverge/internal/sandbox"
	"github.com/divergedev/diverge/pkg/auth"
	pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

type mockToken struct{}

func (m *mockToken) ID() string                 { return "test-id" }
func (m *mockToken) Serialize() ([]byte, error) { return []byte("dummy-token"), nil }
func (m *mockToken) Caveats() []auth.Caveat     { return nil }

type mockTokenMinter struct {
	minted bool
}

func (m *mockTokenMinter) Mint(_ context.Context, _ auth.Claims) (auth.Token, error) {
	m.minted = true
	return &mockToken{}, nil
}

func TestAgentTaskReconcilerLifecycle(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-test-1",
			Namespace: "default",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Build a healthcheck",
			Repository: v1alpha1.AgentTaskRepository{
				URL: "https://github.com/org/repo",
			},
			Sandbox: v1alpha1.AgentTaskSandbox{
				Provider: "noop",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(task).
		WithStatusSubresource(&v1alpha1.AgentTask{}).
		Build()

	minter := &mockTokenMinter{}
	reconciler := &AgentTaskReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		SandboxRegistry: pkgsandbox.Providers,
		TokenMinter:     minter,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "task-test-1"}}

	// Step 1: Adds finalizer
	_, err := reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	updatedTask := &v1alpha1.AgentTask{}
	err = fakeClient.Get(ctx, req.NamespacedName, updatedTask)
	require.NoError(t, err)
	assert.Contains(t, updatedTask.Finalizers, agentTaskFinalizer)

	// Step 2: Provisions sandbox
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.True(t, minter.minted)

	err = fakeClient.Get(ctx, req.NamespacedName, updatedTask)
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.AgentTaskPhaseActive, updatedTask.Status.Phase)
	assert.Equal(t, "noop-claim-task-test-1", updatedTask.Status.SandboxClaimRef)
	assert.Equal(t, "noop-pod-task-test-1", updatedTask.Status.SandboxPodName)

	secret := &corev1.Secret{}
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "task-test-1-token"}, secret)
	require.NoError(t, err)
	assert.Equal(t, []byte("dummy-token"), secret.Data["token"])

	// Step 3: Suspended spec pauses execution
	updatedTask.Spec.Suspended = true
	err = fakeClient.Update(ctx, updatedTask)
	require.NoError(t, err)

	res, err := reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.True(t, res.IsZero())

	err = fakeClient.Get(ctx, req.NamespacedName, updatedTask)
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.AgentTaskPhasePaused, updatedTask.Status.Phase)

	// Step 4: Deletion triggers teardown and clears finalizer
	err = fakeClient.Delete(ctx, updatedTask)
	require.NoError(t, err)

	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	err = fakeClient.Get(ctx, req.NamespacedName, updatedTask)
	assert.True(t, apierrors.IsNotFound(err))
}
