package controller

import (
	"context"
	"fmt"
	"time"

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
}

// +kubebuilder:rbac:groups=diverge.io,resources=environments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=diverge.io,resources=environments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=diverge.io,resources=environments/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces;secrets;services;configmaps;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices;destinationrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

func (r *EnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var env divergeiov1alpha1.Environment
	if err := r.Get(ctx, req.NamespacedName, &env); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

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
			if err := r.Notifier.PostEnvironmentCreated(ctx, &env); err != nil {
				logger.Error(err, "failed to post environment created notification")
			}
		}
		r.Recorder.Event(&env, "Normal", "Created", "Environment created")
		return ctrl.Result{}, nil
	}

	// 4. Set ObservedGeneration
	env.Status.ObservedGeneration = env.Generation

	// 5. Ensure namespace
	if err := r.ensureNamespace(ctx, &env); err != nil {
		setCondition(&env, "NamespaceReady", metav1.ConditionFalse, "NamespaceProvisionFailed", err.Error())
		return r.updateStatus(ctx, &env, err)
	}
	setCondition(&env, "NamespaceReady", metav1.ConditionTrue, "NamespaceProvisioned", "Namespace is ready")

	// 6. Ensure database
	dbStatus, err := r.DatabaseProvider.Provision(ctx, &env)
	if err != nil {
		setCondition(&env, "DatabaseReady", metav1.ConditionFalse, "DatabaseProvisionFailed", err.Error())
		return r.updateStatus(ctx, &env, err)
	}
	if dbStatus != nil && dbStatus.Ready {
		setCondition(&env, "DatabaseReady", metav1.ConditionTrue, "DatabaseProvisioned", "Database is ready")
		env.Status.DatabaseStatus = dbStatus.Message
	} else {
		setCondition(&env, "DatabaseReady", metav1.ConditionFalse, "DatabaseProvisioning", "Database is provisioning")
	}

	// 7. Ensure routing
	if err := r.Router.Reconcile(ctx, &env); err != nil {
		setCondition(&env, "RoutingReady", metav1.ConditionFalse, "RoutingProvisionFailed", err.Error())
		return r.updateStatus(ctx, &env, err)
	}
	setCondition(&env, "RoutingReady", metav1.ConditionTrue, "RoutingProvisioned", "Routing is ready")
	env.Status.URL = r.Router.GetExternalURL(&env)

	// 8. Ensure services (stub)
	setCondition(&env, "ServicesReady", metav1.ConditionTrue, "ServicesProvisioned", "Services are ready")

	// 10. Derive phase
	newPhase := derivePhase(env.Status.Conditions)
	oldPhase := env.Status.Phase
	env.Status.Phase = newPhase

	if env.Status.Phase == divergeiov1alpha1.PhaseRunning {
		r.Recorder.Event(&env, "Normal", "Running", "Environment is up and running")
	}

	if r.Notifier != nil && oldPhase != newPhase {
		switch newPhase {
		case divergeiov1alpha1.PhaseRunning:
			if err := r.Notifier.PostEnvironmentReady(ctx, &env); err != nil {
				logger.Error(err, "failed to post environment ready notification")
			}
		case divergeiov1alpha1.PhaseFailed:
			if err := r.Notifier.PostEnvironmentFailed(ctx, &env, "Environment failed to deploy"); err != nil {
				logger.Error(err, "failed to post environment failed notification")
			}
		}
	}

	// 11. Check TTL expiry
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
	} else if env.Status.CreatedAt == nil {
		now := metav1.Now()
		env.Status.CreatedAt = &now
	}

	// 12. Update status
	return r.updateStatus(ctx, &env, nil)
}

func (r *EnvironmentReconciler) handleTeardown(ctx context.Context, env *divergeiov1alpha1.Environment) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(env, environmentFinalizer) {
		r.Recorder.Event(env, "Normal", "Terminating", "Teardown started")
		
		if r.Notifier != nil {
			if err := r.Notifier.PostEnvironmentTeardown(ctx, env); err != nil {
				log.FromContext(ctx).Error(err, "failed to post environment teardown notification")
			}
		}

		if err := r.Router.Teardown(ctx, env); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to teardown routing: %w", err)
		}
		
		if err := r.DatabaseProvider.Teardown(ctx, env); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to teardown database: %w", err)
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
	// Stub
	return nil
}

func (r *EnvironmentReconciler) updateStatus(ctx context.Context, env *divergeiov1alpha1.Environment, err error) (ctrl.Result, error) {
	if updateErr := r.Status().Update(ctx, env); updateErr != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", updateErr)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func setCondition(env *divergeiov1alpha1.Environment, condType string, status metav1.ConditionStatus, reason, message string) {
	for i, c := range env.Status.Conditions {
		if c.Type == condType {
			if c.Status != status || c.Reason != reason || c.Message != message {
				env.Status.Conditions[i].Status = status
				env.Status.Conditions[i].Reason = reason
				env.Status.Conditions[i].Message = message
				env.Status.Conditions[i].LastTransitionTime = metav1.Now()
			}
			return
		}
	}
	env.Status.Conditions = append(env.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})
}

func derivePhase(conditions []metav1.Condition) divergeiov1alpha1.EnvironmentPhase {
	if len(conditions) == 0 {
		return divergeiov1alpha1.PhasePending
	}
	allReady := true
	for _, c := range conditions {
		if c.Status == metav1.ConditionFalse {
			return divergeiov1alpha1.PhaseFailed
		}
		if c.Status != metav1.ConditionTrue {
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
