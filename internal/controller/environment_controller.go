package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

// EnvironmentReconciler reconciles a Environment object
type EnvironmentReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=diverge.io,resources=environments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=diverge.io,resources=environments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=diverge.io,resources=environments/finalizers,verbs=update

func (r *EnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var env divergeiov1alpha1.Environment
	if err := r.Get(ctx, req.NamespacedName, &env); err != nil {
		logger.Error(err, "unable to fetch Environment")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("Reconciling Environment", "phase", env.Status.Phase)

	switch env.Status.Phase {
	case "", divergeiov1alpha1.PhasePending:
		return r.reconcilePending(ctx, &env)
	case divergeiov1alpha1.PhaseDeploying:
		return r.reconcileDeploying(ctx, &env)
	case divergeiov1alpha1.PhaseRunning:
		return r.reconcileRunning(ctx, &env)
	case divergeiov1alpha1.PhaseTerminating:
		return r.reconcileTerminating(ctx, &env)
	}

	return ctrl.Result{}, nil
}

func (r *EnvironmentReconciler) reconcilePending(ctx context.Context, env *divergeiov1alpha1.Environment) (ctrl.Result, error) {
	// Transition to Deploying
	env.Status.Phase = divergeiov1alpha1.PhaseDeploying
	if err := r.Status().Update(ctx, env); err != nil {
		return ctrl.Result{}, err
	}
	r.Recorder.Event(env, "Normal", "Deploying", "Started environment deployment")
	return ctrl.Result{}, nil
}

func (r *EnvironmentReconciler) reconcileDeploying(ctx context.Context, env *divergeiov1alpha1.Environment) (ctrl.Result, error) {
	// 1. Provision database
	// 2. Deploy changed services
	// 3. Configure routing

	// Transition to Running
	env.Status.Phase = divergeiov1alpha1.PhaseRunning
	if err := r.Status().Update(ctx, env); err != nil {
		return ctrl.Result{}, err
	}
	r.Recorder.Event(env, "Normal", "Running", "Environment is up and running")
	return ctrl.Result{}, nil
}

func (r *EnvironmentReconciler) reconcileRunning(ctx context.Context, env *divergeiov1alpha1.Environment) (ctrl.Result, error) {
	// Monitor TTL and cleanup if needed
	return ctrl.Result{}, nil
}

func (r *EnvironmentReconciler) reconcileTerminating(ctx context.Context, env *divergeiov1alpha1.Environment) (ctrl.Result, error) {
	// Teardown resources
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *EnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&divergeiov1alpha1.Environment{}).
		Complete(r)
}
