package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
	_ "github.com/divergedev/diverge/internal/sandbox"
	pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

func newAgentTaskTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, v1alpha1.AddToScheme(s))
	return s
}

func TestAgentTaskPreview_CreatesAndLinksEnvironment(t *testing.T) {
	ctx := context.Background()
	s := newAgentTaskTestScheme(t)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fix-checkout-42",
			Namespace: "default",
			UID:       "task-uid-42",
			Annotations: map[string]string{
				"divergedev.com/preview-service": "checkout-api",
				"divergedev.com/preview-image":   "ghcr.io/org/checkout:pr-42",
			},
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Fix checkout panic",
			Repository: v1alpha1.AgentTaskRepository{
				URL:           "https://github.com/org/repo",
				BaseBranch:    "main",
				WorkingBranch: "agent/fix-checkout-42",
			},
			Sandbox: v1alpha1.AgentTaskSandbox{
				Provider: "noop",
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(task).
		WithStatusSubresource(&v1alpha1.AgentTask{}, &v1alpha1.Environment{}).
		Build()

	reconciler := &AgentTaskReconciler{
		Client:          c,
		Scheme:          s,
		SandboxRegistry: pkgsandbox.Providers,
	}

	// 1st reconcile adds finalizer
	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: task.Name, Namespace: task.Namespace}})
	require.NoError(t, err)

	// 2nd reconcile provisions sandbox and linked preview Environment
	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: task.Name, Namespace: task.Namespace}})
	require.NoError(t, err)

	expectedEnvName := linkedPreviewEnvironmentName(task.Name)
	var env v1alpha1.Environment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: expectedEnvName, Namespace: "default"}, &env))

	assert.Equal(t, "agent/fix-checkout-42", env.Spec.Source.Branch)
	require.NotNil(t, env.Spec.ServiceConfig)
	assert.Equal(t, "checkout-api", env.Spec.ServiceConfig.ServiceName)
	assert.Equal(t, "ghcr.io/org/checkout:pr-42", env.Spec.ServiceConfig.Image)
	assert.Equal(t, int32(8080), env.Spec.ServiceConfig.Port)
	assert.Equal(t, task.Name, env.Labels["divergedev.com/agent-task"])
	assert.Equal(t, "diverge-agenttask", env.Labels["divergedev.com/managed-by"])
	require.Len(t, env.OwnerReferences, 1)
	assert.Equal(t, task.Name, env.OwnerReferences[0].Name)
	assert.Equal(t, "AgentTask", env.OwnerReferences[0].Kind)

	// Simulate Environment becoming Running with URL
	env.Status.Phase = v1alpha1.PhaseRunning
	env.Status.URL = "https://fix-checkout-42.preview.example.com"
	require.NoError(t, c.Status().Update(ctx, &env))

	// 3rd reconcile (Active phase) updates task annotations and PreviewEnvironmentReady condition
	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: task.Name, Namespace: task.Namespace}})
	require.NoError(t, err)

	var updatedTask v1alpha1.AgentTask
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: task.Name, Namespace: task.Namespace}, &updatedTask))
	assert.Equal(t, expectedEnvName, updatedTask.Annotations["divergedev.com/preview-environment"])
	assert.Equal(t, "https://fix-checkout-42.preview.example.com", updatedTask.Annotations["divergedev.com/preview-url"])

	cond := meta.FindStatusCondition(updatedTask.Status.Conditions, "PreviewEnvironmentReady")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
}

func TestAgentTaskPreview_DisabledByAnnotation(t *testing.T) {
	ctx := context.Background()
	s := newAgentTaskTestScheme(t)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "no-preview-task",
			Namespace: "default",
			Annotations: map[string]string{
				"divergedev.com/preview-enabled": "false",
			},
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Docs only task",
			Repository: v1alpha1.AgentTaskRepository{
				URL: "https://github.com/org/repo",
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(task).Build()
	reconciler := &AgentTaskReconciler{Client: c, Scheme: s}

	require.NoError(t, reconciler.reconcileLinkedPreviewEnvironment(ctx, task))

	var envList v1alpha1.EnvironmentList
	require.NoError(t, c.List(ctx, &envList))
	assert.Empty(t, envList.Items)
}

func TestAgentTaskPreview_DisablingAfterCreationDeletesLinkedEnvironment(t *testing.T) {
	ctx := context.Background()
	s := newAgentTaskTestScheme(t)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "toggle-preview-task",
			Namespace: "default",
			UID:       "task-uid-toggle",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Toggle preview task",
			Repository: v1alpha1.AgentTaskRepository{
				URL: "https://github.com/org/repo",
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(task).
		WithStatusSubresource(&v1alpha1.AgentTask{}, &v1alpha1.Environment{}).
		Build()
	reconciler := &AgentTaskReconciler{Client: c, Scheme: s}

	// First reconcile with preview enabled creates Environment with default nginx:alpine and port 80
	require.NoError(t, reconciler.reconcileLinkedPreviewEnvironment(ctx, task))

	expectedEnvName := linkedPreviewEnvironmentName(task.Name)
	var env v1alpha1.Environment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: expectedEnvName, Namespace: "default"}, &env))
	require.NotNil(t, env.Spec.ServiceConfig)
	assert.Equal(t, "nginx:alpine", env.Spec.ServiceConfig.Image)
	assert.Equal(t, int32(80), env.Spec.ServiceConfig.Port)

	// Simulate URL present while PhaseFailed -> PreviewEnvironmentReady must remain False
	env.Status.Phase = v1alpha1.PhaseFailed
	env.Status.URL = "https://stale.preview.example.com"
	require.NoError(t, c.Status().Update(ctx, &env))

	require.NoError(t, reconciler.reconcileLinkedPreviewEnvironment(ctx, task))
	cond := meta.FindStatusCondition(task.Status.Conditions, conditionPreviewEnvReady)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "EnvironmentFailed", cond.Reason)

	// Disable preview via annotation -> should delete Environment, clear annotations, remove condition
	task.Annotations[annotationPreviewEnabled] = "false"
	require.NoError(t, c.Update(ctx, task))

	require.NoError(t, reconciler.reconcileLinkedPreviewEnvironment(ctx, task))

	var envList v1alpha1.EnvironmentList
	require.NoError(t, c.List(ctx, &envList))
	assert.Empty(t, envList.Items)
	assert.Empty(t, task.Annotations[annotationPreviewEnvironment])
	assert.Empty(t, task.Annotations[annotationPreviewURL])
	assert.Nil(t, meta.FindStatusCondition(task.Status.Conditions, conditionPreviewEnvReady))
}

func TestAgentTaskPreview_DoesNotOverwriteConflictingControllerOwner(t *testing.T) {
	ctx := context.Background()
	s := newAgentTaskTestScheme(t)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-a",
			Namespace: "default",
			UID:       "uid-a",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Task A",
			Repository: v1alpha1.AgentTaskRepository{
				URL: "https://github.com/org/repo",
			},
		},
	}

	isCtrl := true
	existingEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      linkedPreviewEnvironmentName(task.Name),
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "divergedev.com/v1alpha1",
				Kind:       "AgentTask",
				Name:       "other-task",
				UID:        "uid-other",
				Controller: &isCtrl,
			}},
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(task, existingEnv).Build()
	reconciler := &AgentTaskReconciler{Client: c, Scheme: s}

	err := reconciler.reconcileLinkedPreviewEnvironment(ctx, task)
	require.Error(t, err)

	var fetched v1alpha1.Environment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: existingEnv.Name, Namespace: "default"}, &fetched))
	require.Len(t, fetched.OwnerReferences, 1)
	assert.Equal(t, "other-task", fetched.OwnerReferences[0].Name)
}

func TestAgentTaskPreview_CustomPortAndSandboxIP(t *testing.T) {
	ctx := context.Background()
	s := newAgentTaskTestScheme(t)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "live-sandbox-task",
			Namespace: "default",
			UID:       "uid-live-sandbox",
			Annotations: map[string]string{
				"divergedev.com/preview-port": "3000",
			},
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Live sandbox routing test",
			Repository: v1alpha1.AgentTaskRepository{
				URL:           "https://github.com/org/repo",
				WorkingBranch: "agent/live-sandbox-task",
			},
		},
		Status: v1alpha1.AgentTaskStatus{
			SandboxIP: "10.244.1.42",
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(task).
		WithStatusSubresource(&v1alpha1.AgentTask{}, &v1alpha1.Environment{}).
		Build()
	reconciler := &AgentTaskReconciler{Client: c, Scheme: s}

	require.NoError(t, reconciler.reconcileLinkedPreviewEnvironment(ctx, task))

	expectedEnvName := linkedPreviewEnvironmentName(task.Name)
	var env v1alpha1.Environment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: expectedEnvName, Namespace: "default"}, &env))

	require.NotNil(t, env.Spec.ServiceConfig)
	assert.Equal(t, int32(3000), env.Spec.ServiceConfig.Port)
	assert.Equal(t, "10.244.1.42", env.Annotations["divergedev.com/sandbox-ip"])
}

func TestAgentTaskPreview_HibernationOnPauseAndResume(t *testing.T) {
	ctx := context.Background()
	s := newAgentTaskTestScheme(t)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hibernate-task",
			Namespace: "default",
			UID:       "uid-hibernate-task",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Test preview hibernation on pause and resume",
			Repository: v1alpha1.AgentTaskRepository{
				URL:           "https://github.com/org/repo",
				WorkingBranch: "agent/hibernate-task",
			},
			Sandbox: v1alpha1.AgentTaskSandbox{
				Provider: "noop",
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(task).
		WithStatusSubresource(&v1alpha1.AgentTask{}, &v1alpha1.Environment{}).
		Build()

	reconciler := &AgentTaskReconciler{
		Client:          c,
		Scheme:          s,
		SandboxRegistry: pkgsandbox.Providers,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: task.Name, Namespace: task.Namespace}}

	// 1. Add finalizer + provision sandbox and linked preview environment
	_, err := reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	expectedEnvName := linkedPreviewEnvironmentName(task.Name)
	var env v1alpha1.Environment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: expectedEnvName, Namespace: "default"}, &env))
	assert.Empty(t, env.Annotations["divergedev.com/hibernated"])

	// 2. Pause task via Spec.Suspended = true -> linked Environment gets divergedev.com/hibernated=true
	var updatedTask v1alpha1.AgentTask
	require.NoError(t, c.Get(ctx, req.NamespacedName, &updatedTask))
	updatedTask.Spec.Suspended = true
	require.NoError(t, c.Update(ctx, &updatedTask))

	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: expectedEnvName, Namespace: "default"}, &env))
	assert.Equal(t, "true", env.Annotations["divergedev.com/hibernated"])

	// 3. Resume task via Spec.Suspended = false -> divergedev.com/hibernated annotation is removed
	require.NoError(t, c.Get(ctx, req.NamespacedName, &updatedTask))
	updatedTask.Spec.Suspended = false
	require.NoError(t, c.Update(ctx, &updatedTask))

	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: expectedEnvName, Namespace: "default"}, &env))
	assert.Empty(t, env.Annotations["divergedev.com/hibernated"])
}
