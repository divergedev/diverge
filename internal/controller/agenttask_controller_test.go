package controller

import (
	"context"
	"io"
	"testing"
	"time"

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
	"github.com/divergedev/diverge/pkg/registry"
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

type trackingTaskNotifier struct {
	transitions []string
}

func (n *trackingTaskNotifier) NotifyPhaseChange(_ context.Context, _ *v1alpha1.AgentTask, oldPhase, newPhase v1alpha1.AgentTaskPhase) error {
	n.transitions = append(n.transitions, string(oldPhase)+"->"+string(newPhase))
	return nil
}

type trackingLifecycleHook struct {
	preProvision  bool
	postProvision bool
	preTeardown   bool
}

func (h *trackingLifecycleHook) PreProvision(_ context.Context, _ *v1alpha1.AgentTask) error {
	h.preProvision = true
	return nil
}

func (h *trackingLifecycleHook) PostProvision(_ context.Context, _ *v1alpha1.AgentTask, _ *pkgsandbox.SandboxResult) error {
	h.postProvision = true
	return nil
}

func (h *trackingLifecycleHook) PreTeardown(_ context.Context, _ *v1alpha1.AgentTask) error {
	h.preTeardown = true
	return nil
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

	// Step 3b: Un-suspending transitions Paused -> Active
	updatedTask.Spec.Suspended = false
	err = fakeClient.Update(ctx, updatedTask)
	require.NoError(t, err)

	res, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 1*time.Second, res.RequeueAfter)

	err = fakeClient.Get(ctx, req.NamespacedName, updatedTask)
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.AgentTaskPhaseActive, updatedTask.Status.Phase)

	// Step 4: Deletion triggers teardown and clears finalizer
	err = fakeClient.Delete(ctx, updatedTask)
	require.NoError(t, err)

	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	err = fakeClient.Get(ctx, req.NamespacedName, updatedTask)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestAgentTaskReconcilerWithDecoupledInterfaces(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-decoupled",
			Namespace: "default",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Test decoupled interfaces",
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
	memStore := auth.NewInMemoryTokenStore()
	notifier := &trackingTaskNotifier{}
	hooks := &trackingLifecycleHook{}

	reconciler := &AgentTaskReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		SandboxRegistry: pkgsandbox.Providers,
		TokenMinter:     minter,
		TokenStore:      memStore,
		Notifier:        notifier,
		LifecycleHook:   hooks,
		FeatureGate:     &StaticFeatureGate{Enabled: true},
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "task-decoupled"}}

	// 1. Add finalizer
	_, err := reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	// 2. Provision with hooks & in-memory token store
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	assert.True(t, hooks.preProvision)
	assert.True(t, hooks.postProvision)

	// Check token stored in in-memory store rather than k8s secret
	tok, err := memStore.GetToken(ctx, "task-decoupled", "default")
	require.NoError(t, err)
	assert.Equal(t, []byte("dummy-token"), tok)

	assert.Contains(t, notifier.transitions, "->Active")

	// 3. Dynamic Feature Gate Check: disable feature gate
	reconciler.FeatureGate = &StaticFeatureGate{Enabled: false}
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	updatedTask := &v1alpha1.AgentTask{}
	err = fakeClient.Get(ctx, req.NamespacedName, updatedTask)
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.AgentTaskPhasePaused, updatedTask.Status.Phase)
	assert.Contains(t, updatedTask.Status.Message, "feature gate 'agent_sandbox' is disabled")

	// 4. Teardown triggers PreTeardown hook and deletes token from store
	err = fakeClient.Delete(ctx, updatedTask)
	require.NoError(t, err)

	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	assert.True(t, hooks.preTeardown)
	_, err = memStore.GetToken(ctx, "task-decoupled", "default")
	assert.ErrorIs(t, err, auth.ErrTokenNotFound)
}

func TestAgentTaskReconcilerUnknownProviderFailsClosed(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "task-unknown-provider",
			Namespace:  "default",
			Finalizers: []string{agentTaskFinalizer},
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Build a healthcheck",
			Repository: v1alpha1.AgentTaskRepository{
				URL: "https://github.com/org/repo",
			},
			Sandbox: v1alpha1.AgentTaskSandbox{
				Provider: "unregistered-provider",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(task).
		WithStatusSubresource(&v1alpha1.AgentTask{}).
		Build()

	reconciler := &AgentTaskReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		SandboxRegistry: pkgsandbox.Providers,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "task-unknown-provider"}}
	_, err := reconciler.Reconcile(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unregistered-provider")

	updatedTask := &v1alpha1.AgentTask{}
	err = fakeClient.Get(ctx, req.NamespacedName, updatedTask)
	require.NoError(t, err)
	assert.Equal(t, v1alpha1.AgentTaskPhaseFailed, updatedTask.Status.Phase)
	assert.Contains(t, updatedTask.Status.Message, "unregistered-provider")
}

type flakySandboxProvider struct {
	failFirst bool
	attempts  int
	tornDown  bool
}

func (f *flakySandboxProvider) Provision(_ context.Context, task *v1alpha1.AgentTask) (*pkgsandbox.SandboxResult, error) {
	f.attempts++
	if f.failFirst && f.attempts == 1 {
		return nil, assert.AnError
	}
	return &pkgsandbox.SandboxResult{
		ClaimName: "claim-" + task.Name,
		PodName:   "pod-" + task.Name,
		PodIP:     "10.0.0.42",
		Ready:     true,
		Message:   "Ready",
	}, nil
}

func (f *flakySandboxProvider) Teardown(_ context.Context, _ *v1alpha1.AgentTask) error {
	f.tornDown = true
	return nil
}

func (f *flakySandboxProvider) Status(_ context.Context, task *v1alpha1.AgentTask) (*pkgsandbox.SandboxStatus, error) {
	return &pkgsandbox.SandboxStatus{
		ClaimName: "claim-" + task.Name,
		PodName:   "pod-" + task.Name,
		PodIP:     "10.0.0.42",
		Phase:     "Running",
		Ready:     true,
	}, nil
}

func (f *flakySandboxProvider) StreamLogs(_ context.Context, _ *v1alpha1.AgentTask, _ pkgsandbox.LogOptions) (io.ReadCloser, error) {
	return nil, nil
}

func TestAgentTaskReconcilerBudgetTimeoutAndTransientRetry(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	reg := registry.New[pkgsandbox.SandboxProvider]("sandbox")
	flaky := &flakySandboxProvider{failFirst: true}
	reg.Register("flaky", registry.Provider[pkgsandbox.SandboxProvider]{
		Create: func(_ registry.Deps) (pkgsandbox.SandboxProvider, error) {
			return flaky, nil
		},
	})

	rootKey := []byte("0123456789abcdef0123456789abcdef")
	macProvider := auth.NewMacaroonProvider(rootKey)
	memStore := auth.NewInMemoryTokenStore()

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "task-limits",
			Namespace:         "default",
			Finalizers:        []string{agentTaskFinalizer},
			CreationTimestamp: metav1.NewTime(time.Now()),
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective:     "Fix memory leak",
			BudgetUSD:     "5.00",
			BudgetTokens:  100000,
			MaxIterations: 10,
			Repository: v1alpha1.AgentTaskRepository{
				URL:        "https://github.com/org/repo",
				BaseBranch: "main",
			},
			Sandbox: v1alpha1.AgentTaskSandbox{
				Provider:       "flaky",
				TimeoutSeconds: 3600,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(task).
		WithStatusSubresource(&v1alpha1.AgentTask{}).
		Build()

	reconciler := &AgentTaskReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		SandboxRegistry: reg,
		TokenMinter:     macProvider,
		TokenStore:      memStore,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "task-limits"}}

	// 1. First Provision attempt fails transiently -> remains Pending (not terminal Failed) and requeues
	res, err := reconciler.Reconcile(ctx, req)
	require.Error(t, err)
	assert.Equal(t, 10*time.Second, res.RequeueAfter)

	updated := &v1alpha1.AgentTask{}
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	assert.Equal(t, v1alpha1.AgentTaskPhasePending, updated.Status.Phase)
	assert.Contains(t, updated.Status.Message, "retrying")

	// 2. Second Provision attempt succeeds -> transitions to Active
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	assert.Equal(t, v1alpha1.AgentTaskPhaseActive, updated.Status.Phase)
	assert.False(t, memStore.IsRevoked(ctx, "task-limits", "default"))

	// 3. Simulate CostUSD reaching BudgetUSD ($5.00 / $5.00) -> auto-pauses and revokes token
	updated.Status.CostUSD = "5.00"
	require.NoError(t, fakeClient.Status().Update(ctx, updated))

	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	assert.Equal(t, v1alpha1.AgentTaskPhasePaused, updated.Status.Phase)
	assert.Contains(t, updated.Status.Message, "Reached USD budget limit")
	assert.True(t, memStore.IsRevoked(ctx, "task-limits", "default"))

	// Reconciling while still over budget stays Paused (no infinite loop)
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	assert.Equal(t, v1alpha1.AgentTaskPhasePaused, updated.Status.Phase)

	// 4. Bump BudgetUSD to $10.00 -> resumes, re-mints unrevoked token with new $10.00 MaxCostUSD
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	updated.Spec.BudgetUSD = "10.00"
	require.NoError(t, fakeClient.Update(ctx, updated))

	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	assert.Equal(t, v1alpha1.AgentTaskPhaseActive, updated.Status.Phase)
	assert.False(t, memStore.IsRevoked(ctx, "task-limits", "default"))

	rawTok, err := memStore.GetToken(ctx, "task-limits", "default")
	require.NoError(t, err)
	_, claims, err := macProvider.VerifyAndExtract(ctx, rawTok)
	require.NoError(t, err)
	assert.InDelta(t, 10.00, claims.MaxCostUSD, 0.001)
	assert.Equal(t, "default", claims.Namespace)
	assert.Contains(t, claims.AllowedTools, "diverge_verify")

	// 5. Simulate TimeoutSeconds elapsed -> tears down sandbox, revokes token, and transitions to Failed
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	updated.Spec.Sandbox.TimeoutSeconds = 1
	updated.CreationTimestamp = metav1.NewTime(time.Now().Add(-10 * time.Second))
	require.NoError(t, fakeClient.Update(ctx, updated))

	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	assert.Equal(t, v1alpha1.AgentTaskPhaseFailed, updated.Status.Phase)
	assert.Contains(t, updated.Status.Message, "timed out")
	assert.True(t, flaky.tornDown)
	assert.True(t, memStore.IsRevoked(ctx, "task-limits", "default"))
}

type failingTokenMinter struct {
	err error
}

func (f *failingTokenMinter) Mint(_ context.Context, _ auth.Claims) (auth.Token, error) {
	return nil, f.err
}

type failingSaveTokenStore struct {
	*auth.InMemoryTokenStore
	saveErr error
}

func (f *failingSaveTokenStore) SaveToken(ctx context.Context, taskID, namespace string, token []byte) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	return f.InMemoryTokenStore.SaveToken(ctx, taskID, namespace, token)
}

func TestAgentTaskReconcilerResumeStaysPausedWhenTokenMintFails(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "task-resume-fail",
			Namespace:  "default",
			Finalizers: []string{agentTaskFinalizer},
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Resume test",
			Suspended: false,
			Sandbox: v1alpha1.AgentTaskSandbox{
				Provider: "noop",
			},
		},
		Status: v1alpha1.AgentTaskStatus{
			Phase:          v1alpha1.AgentTaskPhasePaused,
			SandboxPodName: "noop-pod-task-resume-fail",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(task).
		WithStatusSubresource(&v1alpha1.AgentTask{}).
		Build()

	reconciler := &AgentTaskReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		SandboxRegistry: pkgsandbox.Providers,
		TokenMinter:     &failingTokenMinter{err: assert.AnError},
		TokenStore:      auth.NewInMemoryTokenStore(),
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "task-resume-fail"}}
	_, err := reconciler.Reconcile(ctx, req)
	require.Error(t, err)

	updated := &v1alpha1.AgentTask{}
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	assert.Equal(t, v1alpha1.AgentTaskPhasePaused, updated.Status.Phase)

	// Also verify SaveToken persistence failure keeps the task Paused
	reconciler.TokenMinter = auth.NewMacaroonProvider([]byte("0123456789abcdef0123456789abcdef"))
	reconciler.TokenStore = &failingSaveTokenStore{
		InMemoryTokenStore: auth.NewInMemoryTokenStore(),
		saveErr:            assert.AnError,
	}
	_, err = reconciler.Reconcile(ctx, req)
	require.Error(t, err)
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	assert.Equal(t, v1alpha1.AgentTaskPhasePaused, updated.Status.Phase)
}

type failingRevokeTokenStore struct {
	*auth.InMemoryTokenStore
	revokeErr error
}

func (f *failingRevokeTokenStore) RevokeToken(ctx context.Context, taskID, namespace string) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	return f.InMemoryTokenStore.RevokeToken(ctx, taskID, namespace)
}

func TestAgentTaskReconcilerPausePersistsBeforeRevokeAndRetries(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "task-pause-retry",
			Namespace:  "default",
			Finalizers: []string{agentTaskFinalizer},
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Pause retry test",
			Suspended: true,
			Sandbox: v1alpha1.AgentTaskSandbox{
				Provider: "noop",
			},
		},
		Status: v1alpha1.AgentTaskStatus{
			Phase: v1alpha1.AgentTaskPhaseActive,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(task).
		WithStatusSubresource(&v1alpha1.AgentTask{}).
		Build()

	baseStore := auth.NewInMemoryTokenStore()
	require.NoError(t, baseStore.SaveToken(ctx, task.Name, task.Namespace, []byte("tok")))
	store := &failingRevokeTokenStore{
		InMemoryTokenStore: baseStore,
		revokeErr:          assert.AnError,
	}

	reconciler := &AgentTaskReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		SandboxRegistry: pkgsandbox.Providers,
		TokenStore:      store,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "task-pause-retry"}}

	// First reconcile: RevokeToken fails, but status is persisted as Paused before revocation
	_, err := reconciler.Reconcile(ctx, req)
	require.Error(t, err)

	updated := &v1alpha1.AgentTask{}
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	assert.Equal(t, v1alpha1.AgentTaskPhasePaused, updated.Status.Phase)
	assert.False(t, store.IsRevoked(ctx, task.Name, task.Namespace))

	// Second reconcile: already Paused, retries revocation and succeeds
	store.revokeErr = nil
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.True(t, store.IsRevoked(ctx, task.Name, task.Namespace))
}

type failingDeleteTokenStore struct {
	*auth.InMemoryTokenStore
	deleteErr error
}

func (f *failingDeleteTokenStore) DeleteToken(ctx context.Context, taskID, namespace string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	return f.InMemoryTokenStore.DeleteToken(ctx, taskID, namespace)
}

func TestAgentTaskReconcilerDeletionBlocksFinalizerOnDeleteTokenError(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "task-delete-token-err",
			Namespace:  "default",
			Finalizers: []string{agentTaskFinalizer},
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Delete token retry test",
			Sandbox: v1alpha1.AgentTaskSandbox{
				Provider: "noop",
			},
		},
		Status: v1alpha1.AgentTaskStatus{
			Phase: v1alpha1.AgentTaskPhaseActive,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(task).
		WithStatusSubresource(&v1alpha1.AgentTask{}).
		Build()

	baseStore := auth.NewInMemoryTokenStore()
	require.NoError(t, baseStore.SaveToken(ctx, task.Name, task.Namespace, []byte("secret-tok")))
	store := &failingDeleteTokenStore{
		InMemoryTokenStore: baseStore,
		deleteErr:          assert.AnError,
	}

	reconciler := &AgentTaskReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		SandboxRegistry: pkgsandbox.Providers,
		TokenStore:      store,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "task-delete-token-err"}}

	require.NoError(t, fakeClient.Delete(ctx, task))

	// First reconcile: DeleteToken fails transiently -> finalizer must remain, requeue after 5s
	res, err := reconciler.Reconcile(ctx, req)
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, 5*time.Second, res.RequeueAfter)

	stillThere := &v1alpha1.AgentTask{}
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, stillThere))
	assert.Contains(t, stillThere.Finalizers, agentTaskFinalizer)

	// Second reconcile: DeleteToken succeeds -> finalizer removed and object deleted
	store.deleteErr = nil
	res, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.True(t, res.IsZero())

	err = fakeClient.Get(ctx, req.NamespacedName, stillThere)
	assert.True(t, apierrors.IsNotFound(err))
}

type trackingSuspenderProvider struct {
	provisionCalls int
	suspendCalls   int
	resumeCalls    int
	phase          string
	ready          bool
}

func (p *trackingSuspenderProvider) Provision(_ context.Context, task *v1alpha1.AgentTask) (*pkgsandbox.SandboxResult, error) {
	p.provisionCalls++
	p.phase = "Running"
	p.ready = true
	return &pkgsandbox.SandboxResult{
		ClaimName: "claim-" + task.Name,
		PodName:   "pod-" + task.Name,
		Ready:     true,
	}, nil
}

func (p *trackingSuspenderProvider) Teardown(_ context.Context, _ *v1alpha1.AgentTask) error {
	return nil
}

func (p *trackingSuspenderProvider) Status(_ context.Context, task *v1alpha1.AgentTask) (*pkgsandbox.SandboxStatus, error) {
	return &pkgsandbox.SandboxStatus{
		ClaimName: "claim-" + task.Name,
		PodName:   "pod-" + task.Name,
		Phase:     p.phase,
		Ready:     p.ready,
	}, nil
}

func (p *trackingSuspenderProvider) StreamLogs(_ context.Context, _ *v1alpha1.AgentTask, _ pkgsandbox.LogOptions) (io.ReadCloser, error) {
	return nil, nil
}

func (p *trackingSuspenderProvider) Suspend(_ context.Context, _ *v1alpha1.AgentTask) error {
	p.suspendCalls++
	p.phase = "Suspended"
	p.ready = false
	return nil
}

func (p *trackingSuspenderProvider) Resume(_ context.Context, _ *v1alpha1.AgentTask) error {
	p.resumeCalls++
	p.phase = "Running"
	p.ready = true
	return nil
}

func TestAgentTaskReconciler_SuspendResumeLinkedPreviewAndListTasksTool(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	reg := registry.New[pkgsandbox.SandboxProvider]("test-suspender")
	suspender := &trackingSuspenderProvider{}
	reg.Register("suspender", registry.Provider[pkgsandbox.SandboxProvider]{
		Create: func(_ registry.Deps) (pkgsandbox.SandboxProvider, error) {
			return suspender, nil
		},
		Description: "test suspender provider",
	})

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-suspend-preview",
			Namespace: "default",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Test suspend, resume, preview hibernation, and diverge_list_tasks",
			BudgetUSD: "5.00",
			Repository: v1alpha1.AgentTaskRepository{
				URL: "https://github.com/org/repo",
			},
			Sandbox: v1alpha1.AgentTaskSandbox{
				Provider: "suspender",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(task).
		WithStatusSubresource(&v1alpha1.AgentTask{}, &v1alpha1.Environment{}).
		Build()

	macProvider := auth.NewMacaroonProvider([]byte("0123456789abcdef0123456789abcdef"))
	memStore := auth.NewInMemoryTokenStore()

	reconciler := &AgentTaskReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		SandboxRegistry: reg,
		TokenMinter:     macProvider,
		TokenStore:      memStore,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "task-suspend-preview"}}

	// 1. Add finalizer + provision + active reconcile to create linked preview Environment
	_, err := reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 1, suspender.provisionCalls)
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	// Verify minted token includes "diverge_list_tasks" in AllowedTools
	tokBytes, err := memStore.GetToken(ctx, task.Name, task.Namespace)
	require.NoError(t, err)
	_, claims, err := macProvider.VerifyAndExtract(ctx, tokBytes)
	require.NoError(t, err)
	assert.Contains(t, claims.AllowedTools, "diverge_list_tasks")

	// Verify linked preview Environment exists and is not hibernated
	envKey := types.NamespacedName{Namespace: "default", Name: linkedPreviewEnvironmentName(task.Name)}
	env := &v1alpha1.Environment{}
	require.NoError(t, fakeClient.Get(ctx, envKey, env))
	assert.Empty(t, env.Annotations["divergedev.com/hibernated"])

	// 2. Pause via Spec.Suspended = true -> calls Suspend() and hibernates linked preview Environment
	updated := &v1alpha1.AgentTask{}
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	updated.Spec.Suspended = true
	require.NoError(t, fakeClient.Update(ctx, updated))

	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 1, suspender.suspendCalls)
	require.NoError(t, fakeClient.Get(ctx, envKey, env))
	assert.Equal(t, "true", env.Annotations["divergedev.com/hibernated"])

	// 3. Resume via Spec.Suspended = false -> calls Resume() and un-hibernates linked preview Environment
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	updated.Spec.Suspended = false
	require.NoError(t, fakeClient.Update(ctx, updated))

	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 1, suspender.resumeCalls)
	require.NoError(t, fakeClient.Get(ctx, envKey, env))
	assert.Empty(t, env.Annotations["divergedev.com/hibernated"])

	// 4. Pause again, simulate terminated pod ("Failed"), and verify Resume transitions to Pending and re-provisions
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	updated.Spec.Suspended = true
	require.NoError(t, fakeClient.Update(ctx, updated))
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	suspender.phase = "Failed"
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	updated.Spec.Suspended = false
	require.NoError(t, fakeClient.Update(ctx, updated))
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 2, suspender.provisionCalls, "Resume must re-provision when sandbox pod is in Failed phase")
}
