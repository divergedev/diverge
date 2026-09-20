package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// AgentTaskReconciler reconciles an AgentTask object.
type AgentTaskReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	SandboxRegistry *registry.Registry[pkgsandbox.SandboxProvider]
	TokenMinter     auth.Minter
}

// +kubebuilder:rbac:groups=divergedev.com,resources=agenttasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=divergedev.com,resources=agenttasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=divergedev.com,resources=agenttasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=extensions.agents.x-k8s.io,resources=sandboxclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agents.x-k8s.io,resources=sandboxes,verbs=get;list;watch

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
			provider, err := r.resolveSandboxProvider(task)
			if err == nil && provider != nil {
				if err := provider.Teardown(ctx, task); err != nil {
					logger.Error(err, "Failed to teardown sandbox")
					return ctrl.Result{RequeueAfter: 5 * time.Second}, err
				}
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

	// 3. Handle Suspended State
	if task.Spec.Suspended {
		if task.Status.Phase != v1alpha1.AgentTaskPhasePaused {
			task.Status.Phase = v1alpha1.AgentTaskPhasePaused
			task.Status.Message = "Task execution paused by spec.suspended"
			if err := r.Status().Update(ctx, task); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// 4. Resolve Sandbox Provider
	provider, err := r.resolveSandboxProvider(task)
	if err != nil {
		task.Status.Phase = v1alpha1.AgentTaskPhaseFailed
		task.Status.Message = fmt.Sprintf("Failed to resolve sandbox provider: %v", err)
		_ = r.Status().Update(ctx, task)
		return ctrl.Result{}, err
	}

	// 5. Lifecycle State Machine
	switch task.Status.Phase {
	case "", v1alpha1.AgentTaskPhasePending:
		logger.Info("Provisioning sandbox for AgentTask", "task", task.Name)

		// Mint capability token and persist to Secret if minter configured
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
					secret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      fmt.Sprintf("%s-token", task.Name),
							Namespace: task.Namespace,
							Labels: map[string]string{
								"divergedev.com/agent-task":    task.Name,
								"app.kubernetes.io/managed-by": "diverge",
							},
						},
						Data: map[string][]byte{
							"token": tokBytes,
						},
					}
					if r.Scheme != nil {
						_ = controllerutil.SetControllerReference(task, secret, r.Scheme)
					}
					if err := r.Create(ctx, secret); err != nil && !apierrors.IsAlreadyExists(err) {
						logger.Error(err, "Failed to persist task token Secret")
					}
				}
			}
		}

		res, err := provider.Provision(ctx, task)
		if err != nil {
			task.Status.Phase = v1alpha1.AgentTaskPhaseFailed
			task.Status.Message = fmt.Sprintf("Provisioning failed: %v", err)
			_ = r.Status().Update(ctx, task)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}

		task.Status.SandboxClaimRef = res.ClaimName
		task.Status.SandboxPodName = res.PodName
		task.Status.SandboxIP = res.PodIP
		task.Status.Message = res.Message
		if res.Ready {
			task.Status.Phase = v1alpha1.AgentTaskPhaseActive
		} else {
			task.Status.Phase = v1alpha1.AgentTaskPhaseProvisioning
		}

		if err := r.Status().Update(ctx, task); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil

	case v1alpha1.AgentTaskPhaseProvisioning:
		sbStatus, err := provider.Status(ctx, task)
		if err != nil {
			logger.Error(err, "Error polling sandbox status")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		task.Status.SandboxPodName = sbStatus.PodName
		task.Status.SandboxIP = sbStatus.PodIP
		task.Status.Message = sbStatus.Message
		if sbStatus.Ready {
			task.Status.Phase = v1alpha1.AgentTaskPhaseActive
			task.Status.Message = "Sandbox active and ready"
		}

		if err := r.Status().Update(ctx, task); err != nil {
			return ctrl.Result{}, err
		}
		if !sbStatus.Ready {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil

	case v1alpha1.AgentTaskPhaseActive:
		// Check iteration limits
		if task.Spec.MaxIterations > 0 && task.Status.Iteration >= task.Spec.MaxIterations {
			task.Status.Phase = v1alpha1.AgentTaskPhasePaused
			task.Status.Message = fmt.Sprintf("Reached max iterations limit (%d); paused", task.Spec.MaxIterations)
			if err := r.Status().Update(ctx, task); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		// Periodic health check
		sbStatus, err := provider.Status(ctx, task)
		if err == nil && sbStatus != nil {
			if sbStatus.PodName != "" {
				task.Status.SandboxPodName = sbStatus.PodName
			}
			if sbStatus.PodIP != "" {
				task.Status.SandboxIP = sbStatus.PodIP
			}
		}

		_ = r.Status().Update(ctx, task)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil

	case v1alpha1.AgentTaskPhasePaused, v1alpha1.AgentTaskPhaseCompleted, v1alpha1.AgentTaskPhaseFailed:
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
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
		// Fallback to noop if agent-sandbox is not compiled or available
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
