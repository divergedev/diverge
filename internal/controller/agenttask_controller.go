package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/auth"
	"github.com/divergedev/diverge/pkg/registry"
	pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

const agentTaskFinalizer = "divergedev.com/agenttask-finalizer"

// TaskNotifier notifies external observers when an AgentTask changes phases.
type TaskNotifier interface {
	NotifyPhaseChange(ctx context.Context, task *v1alpha1.AgentTask, oldPhase, newPhase v1alpha1.AgentTaskPhase) error
}

// NoopTaskNotifier is a default no-op implementation of TaskNotifier.
type NoopTaskNotifier struct{}

// NotifyPhaseChange does nothing.
func (n *NoopTaskNotifier) NotifyPhaseChange(_ context.Context, _ *v1alpha1.AgentTask, _, _ v1alpha1.AgentTaskPhase) error {
	return nil
}

// SandboxLifecycleHook allows executing custom logic before/after provisioning and before teardown.
type SandboxLifecycleHook interface {
	PreProvision(ctx context.Context, task *v1alpha1.AgentTask) error
	PostProvision(ctx context.Context, task *v1alpha1.AgentTask, res *pkgsandbox.SandboxResult) error
	PreTeardown(ctx context.Context, task *v1alpha1.AgentTask) error
}

// FeatureGate provides dynamic feature enablement queries.
type FeatureGate interface {
	IsEnabled(ctx context.Context, feature string) bool
}

// StaticFeatureGate is a static implementation of FeatureGate.
type StaticFeatureGate struct {
	Enabled bool
}

// IsEnabled returns the configured static enablement state.
func (g *StaticFeatureGate) IsEnabled(_ context.Context, _ string) bool {
	return g.Enabled
}

// AgentTaskReconciler reconciles an AgentTask object.
type AgentTaskReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	SandboxRegistry *registry.Registry[pkgsandbox.SandboxProvider]
	TokenMinter     auth.Minter
	TokenStore      auth.TokenStore
	TokenAuditor    auth.TokenAuditor
	Notifier        TaskNotifier
	LifecycleHook   SandboxLifecycleHook
	FeatureGate     FeatureGate
}

// +kubebuilder:rbac:groups=divergedev.com,resources=agenttasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=divergedev.com,resources=agenttasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=divergedev.com,resources=agenttasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=extensions.agents.x-k8s.io,resources=sandboxclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agents.x-k8s.io,resources=sandboxes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile manages the AgentTask lifecycle.
func (r *AgentTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("agenttask", req.NamespacedName)

	task := &v1alpha1.AgentTask{}
	if err := r.Get(ctx, req.NamespacedName, task); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// 1. Handle Deletion
	if !task.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(task, agentTaskFinalizer) {
			logger.Info("Tearing down AgentTask sandbox", "task", task.Name)
			if r.LifecycleHook != nil {
				if err := r.LifecycleHook.PreTeardown(ctx, task); err != nil {
					logger.Error(err, "PreTeardown lifecycle hook failed")
				}
			}

			provider, err := r.resolveSandboxProvider(task)
			if err == nil && provider != nil {
				if err := provider.Teardown(ctx, task); err != nil {
					logger.Error(err, "Failed to teardown sandbox")
					return ctrl.Result{RequeueAfter: 5 * time.Second}, err
				}
			}

			store := r.resolveTokenStore()
			_ = store.DeleteToken(ctx, task.Name, task.Namespace)

			if r.TokenAuditor != nil {
				_ = r.TokenAuditor.RecordEvent(ctx, auth.AuthEvent{
					Type:      auth.EventRevoke,
					TaskID:    task.Name,
					Namespace: task.Namespace,
					Principal: "diverge-controller",
					Success:   true,
					Timestamp: time.Now(),
				})
			}

			controllerutil.RemoveFinalizer(task, agentTaskFinalizer)
			if err := r.Update(ctx, task); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// 2. Ensure Finalizer
	if !controllerutil.ContainsFinalizer(task, agentTaskFinalizer) {
		controllerutil.AddFinalizer(task, agentTaskFinalizer)
		if err := r.Update(ctx, task); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// 3. Dynamic Feature Gate Check
	if r.FeatureGate != nil && !r.FeatureGate.IsEnabled(ctx, "agent_sandbox") {
		if task.Status.Phase != v1alpha1.AgentTaskPhasePaused {
			patch := client.MergeFrom(task.DeepCopy())
			task.Status.Phase = v1alpha1.AgentTaskPhasePaused
			task.Status.Message = "Agent sandbox execution paused: feature gate 'agent_sandbox' is disabled"
			_ = r.Status().Patch(ctx, task, patch)
		}
		return ctrl.Result{}, nil
	}

	// 4. Handle Suspended State
	if task.Spec.Suspended {
		if task.Status.Phase != v1alpha1.AgentTaskPhasePaused {
			patch := client.MergeFrom(task.DeepCopy())
			oldPhase := task.Status.Phase
			task.Status.Phase = v1alpha1.AgentTaskPhasePaused
			task.Status.Message = "Task execution paused by spec.suspended"
			if r.Notifier != nil && oldPhase != task.Status.Phase {
				_ = r.Notifier.NotifyPhaseChange(ctx, task, oldPhase, task.Status.Phase)
			}
			if err := r.Status().Patch(ctx, task, patch); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// 5. Resolve Sandbox Provider
	provider, err := r.resolveSandboxProvider(task)
	if err != nil {
		patch := client.MergeFrom(task.DeepCopy())
		oldPhase := task.Status.Phase
		task.Status.Phase = v1alpha1.AgentTaskPhaseFailed
		task.Status.Message = fmt.Sprintf("Failed to resolve sandbox provider: %v", err)
		if r.Notifier != nil && oldPhase != task.Status.Phase {
			_ = r.Notifier.NotifyPhaseChange(ctx, task, oldPhase, task.Status.Phase)
		}
		_ = r.Status().Patch(ctx, task, patch)
		return ctrl.Result{}, err
	}

	// 6. Lifecycle State Machine
	switch task.Status.Phase {
	case "", v1alpha1.AgentTaskPhasePending:
		logger.Info("Provisioning sandbox for AgentTask", "task", task.Name)

		// Mint capability token and persist via TokenStore
		if r.TokenMinter != nil {
			timeoutSec := task.Spec.Sandbox.TimeoutSeconds
			if timeoutSec <= 0 {
				timeoutSec = 3600
			}
			claims := auth.Claims{
				TaskID:        task.Name,
				RepoURL:       task.Spec.Repository.URL,
				Branch:        task.Spec.Repository.BaseBranch,
				AllowedTools:  []string{"diverge_*"},
				AllowedModels: task.Spec.Capabilities,
				MaxTokens:     task.Spec.BudgetTokens,
				ExpiresAt:     time.Now().Add(time.Duration(timeoutSec) * time.Second),
			}
			if task.Spec.BudgetUSD != "" {
				var cost float64
				if _, err := fmt.Sscanf(task.Spec.BudgetUSD, "%f", &cost); err == nil {
					claims.MaxCostUSD = cost
				}
			}

			tok, err := r.TokenMinter.Mint(ctx, claims)
			if err != nil {
				logger.Error(err, "Failed to mint task token")
			} else {
				tokBytes, err := tok.Serialize()
				if err != nil {
					logger.Error(err, "Failed to serialize task token")
				} else {
					store := r.resolveTokenStore()
					if err := store.SaveToken(ctx, task.Name, task.Namespace, tokBytes); err != nil {
						logger.Error(err, "Failed to persist task token via TokenStore")
					}
					if r.TokenAuditor != nil {
						_ = r.TokenAuditor.RecordEvent(ctx, auth.AuthEvent{
							Type:      auth.EventMint,
							TaskID:    task.Name,
							Namespace: task.Namespace,
							Principal: "diverge-controller",
							Success:   true,
							Timestamp: time.Now(),
						})
					}
				}
			}
		}

		if r.LifecycleHook != nil {
			if err := r.LifecycleHook.PreProvision(ctx, task); err != nil {
				logger.Error(err, "PreProvision lifecycle hook failed")
			}
		}

		res, err := provider.Provision(ctx, task)
		if err != nil {
			patch := client.MergeFrom(task.DeepCopy())
			oldPhase := task.Status.Phase
			task.Status.Phase = v1alpha1.AgentTaskPhaseFailed
			task.Status.Message = fmt.Sprintf("Provisioning failed: %v", err)
			if r.Notifier != nil && oldPhase != task.Status.Phase {
				_ = r.Notifier.NotifyPhaseChange(ctx, task, oldPhase, task.Status.Phase)
			}
			_ = r.Status().Patch(ctx, task, patch)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}

		if r.LifecycleHook != nil {
			if err := r.LifecycleHook.PostProvision(ctx, task, res); err != nil {
				logger.Error(err, "PostProvision lifecycle hook failed")
			}
		}

		patch := client.MergeFrom(task.DeepCopy())
		oldPhase := task.Status.Phase
		task.Status.SandboxClaimRef = res.ClaimName
		task.Status.SandboxPodName = res.PodName
		task.Status.SandboxIP = res.PodIP
		task.Status.Message = res.Message
		if res.Ready {
			task.Status.Phase = v1alpha1.AgentTaskPhaseActive
		} else {
			task.Status.Phase = v1alpha1.AgentTaskPhaseProvisioning
		}
		if r.Notifier != nil && oldPhase != task.Status.Phase {
			_ = r.Notifier.NotifyPhaseChange(ctx, task, oldPhase, task.Status.Phase)
		}

		if err := r.Status().Patch(ctx, task, patch); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil

	case v1alpha1.AgentTaskPhaseProvisioning:
		sbStatus, err := provider.Status(ctx, task)
		if err != nil {
			logger.Error(err, "Error polling sandbox status")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		patch := client.MergeFrom(task.DeepCopy())
		oldPhase := task.Status.Phase
		task.Status.SandboxPodName = sbStatus.PodName
		task.Status.SandboxIP = sbStatus.PodIP
		task.Status.Message = sbStatus.Message
		if sbStatus.Ready {
			task.Status.Phase = v1alpha1.AgentTaskPhaseActive
			task.Status.Message = "Sandbox active and ready"
		}
		if r.Notifier != nil && oldPhase != task.Status.Phase {
			_ = r.Notifier.NotifyPhaseChange(ctx, task, oldPhase, task.Status.Phase)
		}

		if err := r.Status().Patch(ctx, task, patch); err != nil {
			return ctrl.Result{}, err
		}
		if !sbStatus.Ready {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil

	case v1alpha1.AgentTaskPhaseActive:
		// Check iteration limits
		if task.Spec.MaxIterations > 0 && task.Status.Iteration >= task.Spec.MaxIterations {
			patch := client.MergeFrom(task.DeepCopy())
			oldPhase := task.Status.Phase
			task.Status.Phase = v1alpha1.AgentTaskPhasePaused
			task.Status.Message = fmt.Sprintf("Reached max iterations limit (%d); paused", task.Spec.MaxIterations)
			if r.Notifier != nil && oldPhase != task.Status.Phase {
				_ = r.Notifier.NotifyPhaseChange(ctx, task, oldPhase, task.Status.Phase)
			}
			if err := r.Status().Patch(ctx, task, patch); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		// Periodic health check
		sbStatus, err := provider.Status(ctx, task)
		patch := client.MergeFrom(task.DeepCopy())
		if err == nil && sbStatus != nil {
			if sbStatus.PodName != "" {
				task.Status.SandboxPodName = sbStatus.PodName
			}
			if sbStatus.PodIP != "" {
				task.Status.SandboxIP = sbStatus.PodIP
			}
		}

		_ = r.Status().Patch(ctx, task, patch)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil

	case v1alpha1.AgentTaskPhasePaused, v1alpha1.AgentTaskPhaseCompleted, v1alpha1.AgentTaskPhaseFailed:
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *AgentTaskReconciler) resolveTokenStore() auth.TokenStore {
	if r.TokenStore != nil {
		return r.TokenStore
	}
	return auth.NewKubernetesSecretTokenStore(r.Client, r.Scheme)
}

func (r *AgentTaskReconciler) resolveSandboxProvider(task *v1alpha1.AgentTask) (pkgsandbox.SandboxProvider, error) {
	providerName := task.Spec.Sandbox.Provider
	if providerName == "" {
		providerName = "agent-sandbox"
	}

	reg := r.SandboxRegistry
	if reg == nil {
		reg = pkgsandbox.Providers
	}

	if !reg.Has(providerName) {
		if reg.Has("noop") {
			return reg.Create("noop", registry.Deps{Client: r.Client, Scheme: r.Scheme})
		}
		return nil, fmt.Errorf("sandbox provider %q not registered", providerName)
	}

	return reg.Create(providerName, registry.Deps{
		Client: r.Client,
		Scheme: r.Scheme,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.AgentTask{}).
		Complete(r)
}
