package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/changeset"
	"github.com/divergedev/diverge/internal/database"
	"github.com/divergedev/diverge/internal/deployer"
	"github.com/divergedev/diverge/internal/notifier"
	"github.com/divergedev/diverge/internal/routing"
)

const environmentFinalizer = "diverge.io/environment-protection"

// EnvironmentReconciler reconciles a Environment object
type EnvironmentReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Recorder         record.EventRecorder
	Router           routing.Router
	DatabaseProvider database.DatabaseProvider
	ChangeDetector   changeset.ChangeDetector
	Notifier         notifier.Notifier
	Deployer         deployer.Deployer
}

// +kubebuilder:rbac:groups=diverge.io,resources=environments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=diverge.io,resources=environments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=diverge.io,resources=environments/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces;secrets;services;configmaps;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices;destinationrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;create;update;patch;delete

func (r *EnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var env divergeiov1alpha1.Environment
	if err := r.Get(ctx, req.NamespacedName, &env); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Capture a pre-mutation baseline for status patch diffs
	statusBase := env.DeepCopy()

	// 2. Handle deletion
	if !env.DeletionTimestamp.IsZero() {
		return r.handleTeardown(ctx, &env)
	}

	// 3. Add finalizer
	if !controllerutil.ContainsFinalizer(&env, environmentFinalizer) {
		controllerutil.AddFinalizer(&env, environmentFinalizer)
		if err := r.Update(ctx, &env); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		if r.Notifier != nil {
			tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := r.Notifier.PostEnvironmentCreated(tCtx, &env); err != nil {
				logger.Error(err, "failed to post environment created notification")
				r.Recorder.Event(&env, "Warning", "NotificationFailed", err.Error())
			}
		}
		r.Recorder.Event(&env, "Normal", "Created", "Environment created")
		return ctrl.Result{}, nil
	}

	// 4. Set ObservedGeneration
	env.Status.ObservedGeneration = env.Generation

	// 5. Ensure namespace
	if err := r.ensureNamespace(ctx, &env); err != nil {
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "NamespaceReady",
			Status:  metav1.ConditionFalse,
			Reason:  "NamespaceProvisionFailed",
			Message: err.Error(),
		})
		if r.Notifier != nil {
			tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if notifyErr := r.Notifier.PostEnvironmentFailed(tCtx, &env, err.Error()); notifyErr != nil {
				logger.Error(notifyErr, "failed to post environment failed notification")
				r.Recorder.Event(&env, "Warning", "NotificationFailed", notifyErr.Error())
			}
		}
		return r.updateStatusWithRequeue(ctx, &env, statusBase, err, 0)
	}
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:    "NamespaceReady",
		Status:  metav1.ConditionTrue,
		Reason:  "NamespaceProvisioned",
		Message: "Namespace is ready",
	})

	// 6. Ensure database
	tCtxDB, cancelDB := context.WithTimeout(ctx, 30*time.Second)
	defer cancelDB()
	dbStatus, err := r.DatabaseProvider.Provision(tCtxDB, &env)
	if err != nil {
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "DatabaseReady",
			Status:  metav1.ConditionFalse,
			Reason:  "DatabaseProvisionFailed",
			Message: err.Error(),
		})
		if r.Notifier != nil {
			tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if notifyErr := r.Notifier.PostEnvironmentFailed(tCtx, &env, err.Error()); notifyErr != nil {
				logger.Error(notifyErr, "failed to post environment failed notification")
				r.Recorder.Event(&env, "Warning", "NotificationFailed", notifyErr.Error())
			}
		}
		return r.updateStatusWithRequeue(ctx, &env, statusBase, err, 0)
	}
	if dbStatus != nil && dbStatus.Ready {
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "DatabaseReady",
			Status:  metav1.ConditionTrue,
			Reason:  "DatabaseProvisioned",
			Message: "Database is ready",
		})
		env.Status.DatabaseStatus = dbStatus.Message
	} else {
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "DatabaseReady",
			Status:  metav1.ConditionFalse,
			Reason:  "DatabaseProvisioning",
			Message: "Database is provisioning",
		})
	}

	// 7. Ensure routing
	tCtxR, cancelR := context.WithTimeout(ctx, 30*time.Second)
	defer cancelR()
	if err := r.Router.Reconcile(tCtxR, &env); err != nil {
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "RoutingReady",
			Status:  metav1.ConditionFalse,
			Reason:  "RoutingProvisionFailed",
			Message: err.Error(),
		})
		if r.Notifier != nil {
			tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if notifyErr := r.Notifier.PostEnvironmentFailed(tCtx, &env, err.Error()); notifyErr != nil {
				logger.Error(notifyErr, "failed to post environment failed notification")
				r.Recorder.Event(&env, "Warning", "NotificationFailed", notifyErr.Error())
			}
		}
		return r.updateStatusWithRequeue(ctx, &env, statusBase, err, 0)
	}
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:    "RoutingReady",
		Status:  metav1.ConditionTrue,
		Reason:  "RoutingProvisioned",
		Message: "Routing is ready",
	})
	env.Status.URL = r.Router.GetExternalURL(&env)

	// 8. Deploy services
	if r.Deployer != nil {
		tCtxD, cancelD := context.WithTimeout(ctx, 30*time.Second)
		defer cancelD()
		if err := r.Deployer.Deploy(tCtxD, &env); err != nil {
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    "ServicesReady",
				Status:  metav1.ConditionFalse,
				Reason:  "DeployFailed",
				Message: err.Error(),
			})
			if r.Notifier != nil {
				tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				if notifyErr := r.Notifier.PostEnvironmentFailed(tCtx, &env, err.Error()); notifyErr != nil {
					logger.Error(notifyErr, "failed to post environment failed notification")
					r.Recorder.Event(&env, "Warning", "NotificationFailed", notifyErr.Error())
				}
			}
			return r.updateStatusWithRequeue(ctx, &env, statusBase, err, 0)
		}
	}
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:    "ServicesReady",
		Status:  metav1.ConditionTrue,
		Reason:  "ServicesDeployed",
		Message: "Services deployed successfully",
	})

	// 10. Derive phase
	newPhase := derivePhase(env.Status.Conditions)
	oldPhase := env.Status.Phase
	env.Status.Phase = newPhase

	if env.Status.Phase == divergeiov1alpha1.PhaseRunning {
		r.Recorder.Event(&env, "Normal", "Running", "Environment is up and running")
	}

	// 11. Check TTL expiry and set timestamps
	if env.Spec.Lifecycle.TTL != nil && env.Status.CreatedAt != nil {
		expiryTime := env.Status.CreatedAt.Add(env.Spec.Lifecycle.TTL.Duration)
		env.Status.ExpiresAt = &metav1.Time{Time: expiryTime}
		if time.Now().After(expiryTime) {
			logger.Info("Environment TTL expired, triggering deletion")
			if err := r.Delete(ctx, &env); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete expired environment: %w", err)
			}
			return ctrl.Result{}, nil
		}
		// C1: Requeue when TTL expires so the controller wakes up to delete
		return r.updateStatusWithRequeue(ctx, &env, statusBase, nil, time.Until(expiryTime))
	} else if env.Status.CreatedAt == nil {
		now := metav1.Now()
		env.Status.CreatedAt = &now
	}

	if r.Notifier != nil && oldPhase != newPhase {
		switch newPhase {
		case divergeiov1alpha1.PhaseRunning:
			tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := r.Notifier.PostEnvironmentReady(tCtx, &env); err != nil {
				logger.Error(err, "failed to post environment ready notification")
				r.Recorder.Event(&env, "Warning", "NotificationFailed", err.Error())
			}
		case divergeiov1alpha1.PhaseFailed:
			tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := r.Notifier.PostEnvironmentFailed(tCtx, &env, "Environment failed to deploy"); err != nil {
				logger.Error(err, "failed to post environment failed notification")
				r.Recorder.Event(&env, "Warning", "NotificationFailed", err.Error())
			}
		}
	}

	// 12. Update status
	return r.updateStatusWithRequeue(ctx, &env, statusBase, nil, 0)
}

func (r *EnvironmentReconciler) handleTeardown(ctx context.Context, env *divergeiov1alpha1.Environment) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(env, environmentFinalizer) {
		r.Recorder.Event(env, "Normal", "Terminating", "Teardown started")

		if r.Notifier != nil {
			tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := r.Notifier.PostEnvironmentTeardown(tCtx, env); err != nil {
				log.FromContext(ctx).Error(err, "failed to post environment teardown notification")
				r.Recorder.Event(env, "Warning", "NotificationFailed", err.Error())
			}
		}

		if r.Deployer != nil {
			tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := r.Deployer.Teardown(tCtx, env); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to teardown deployments: %w", err)
			}
		}

		tCtxR, cancelR := context.WithTimeout(ctx, 30*time.Second)
		defer cancelR()
		if err := r.Router.Teardown(tCtxR, env); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to teardown routing: %w", err)
		}

		tCtxDB, cancelDB := context.WithTimeout(ctx, 30*time.Second)
		defer cancelDB()
		if err := r.DatabaseProvider.Teardown(tCtxDB, env); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to teardown database: %w", err)
		}

		// C4: Wait for ArgoCD Applications to be fully deleted before
		// deleting the namespace, preventing finalizer deadlocks where
		// the namespace enters Terminating but ArgoCD resources still
		// have resources-finalizer.argocd.argoproj.io.
		if env.Spec.Deploy.Namespace == "create" {
			if r.Deployer != nil {
				tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				status, err := r.Deployer.Status(tCtx, env)
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to check deployer status during teardown: %w", err)
				}
				if len(status) > 0 {
					log.FromContext(ctx).Info("Waiting for deployer resources to be fully deleted", "remaining", len(status))
					return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
				}
			}
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: env.PreviewNamespace(),
				},
			}
			if err := r.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to delete namespace: %w", err)
			}
		}

		controllerutil.RemoveFinalizer(env, environmentFinalizer)
		if err := r.Update(ctx, env); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
		}

		r.Recorder.Event(env, "Normal", "Terminated", "Teardown complete")
	}
	return ctrl.Result{}, nil
}

func (r *EnvironmentReconciler) ensureNamespace(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	if env.Spec.Deploy.Namespace == "create" {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: env.PreviewNamespace(),
				Labels: map[string]string{
					"diverge.io/environment": env.Name,
					"diverge.io/managed-by":  "diverge",
				},
			},
		}
		if err := r.Create(ctx, ns); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("failed to create namespace: %w", err)
			}
		}
	}
	// "same" mode: namespace already exists (it's where the CR lives), nothing to do
	return nil
}

// H3: Use Status().Patch() instead of Update() to avoid 409 conflicts.
// statusBase must be a DeepCopy captured before any mutations to env.
func (r *EnvironmentReconciler) updateStatusWithRequeue(ctx context.Context, env *divergeiov1alpha1.Environment, statusBase *divergeiov1alpha1.Environment, err error, requeueAfter time.Duration) (ctrl.Result, error) {
	patch := client.MergeFrom(statusBase)
	if updateErr := r.Status().Patch(ctx, env, patch); updateErr != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", updateErr)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	if requeueAfter > 0 {
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	return ctrl.Result{}, nil
}

func derivePhase(conditions []metav1.Condition) divergeiov1alpha1.EnvironmentPhase {
	if len(conditions) == 0 {
		return divergeiov1alpha1.PhasePending
	}
	allReady := true
	for _, c := range conditions {
		switch c.Status {
		case metav1.ConditionFalse:
			return divergeiov1alpha1.PhaseFailed
		case metav1.ConditionTrue:
			// continue checking
		default:
			// ConditionUnknown or empty — still transitioning
			allReady = false
		}
	}
	if allReady {
		return divergeiov1alpha1.PhaseRunning
	}
	return divergeiov1alpha1.PhaseDeploying
}

// SetupWithManager sets up the controller with the Manager.
func (r *EnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&divergeiov1alpha1.Environment{}).
		Complete(r)
}
