package controller

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/async"
	"github.com/divergedev/diverge/internal/changeset"
	"github.com/divergedev/diverge/internal/deployer"
	"github.com/divergedev/diverge/internal/events"
	"github.com/divergedev/diverge/internal/metrics"
	"github.com/divergedev/diverge/internal/notifier"
	"github.com/divergedev/diverge/internal/routing"
	divtesting "github.com/divergedev/diverge/internal/testing"
	"github.com/divergedev/diverge/pkg/database"
)

const environmentFinalizer = "diverge.io/environment-protection"

// EnvironmentReconciler reconciles a Environment object
type EnvironmentReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Recorder         *events.Recorder
	Router           routing.Router
	DatabaseProvider database.DatabaseProvider
	ChangeDetector   changeset.ChangeDetector
	Notifier         notifier.Notifier
	StatusReporter   notifier.StatusReporter
	Deployer         deployer.Deployer
	TestRunner       divtesting.TestRunner
	AsyncProvisioner async.Provisioner
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
// +kubebuilder:rbac:groups=http.keda.sh,resources=httpscaledobjects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

// Reconcile performs its designated operation.
func (r *EnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	ctx, span := otel.Tracer("diverge").Start(ctx, "Reconcile")
	defer span.End()

	logger := log.FromContext(ctx)

	var env divergeiov1alpha1.Environment
	if err := r.Get(ctx, req.NamespacedName, &env); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	startTime := time.Now()

	// Track reconcile outcome — single defer covers all return paths
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("environment").Observe(time.Since(startTime).Seconds())

		if retErr != nil {
			metrics.ReconcileTotal.WithLabelValues("environment", "error").Inc()
			metrics.ReconcileOutcomes.WithLabelValues("error").Inc()
		} else {
			metrics.ReconcileTotal.WithLabelValues("environment", "success").Inc()
			metrics.ReconcileOutcomes.WithLabelValues("success").Inc()
		}

		// Update TTL gauge if applicable.
		// Skip on deletion path — handleTeardown already deleted the series.
		if env.DeletionTimestamp.IsZero() && env.Status.ExpiresAt != nil {
			remaining := time.Until(env.Status.ExpiresAt.Time).Seconds()
			if remaining < 0 {
				remaining = 0
			}
			metrics.EnvironmentTTLRemaining.WithLabelValues(
				env.Name, env.Namespace,
			).Set(remaining)
		}
	}()

	// Capture a pre-mutation baseline for status patch diffs
	statusBase := env.DeepCopy()

	// 2. Handle deletion
	if !env.DeletionTimestamp.IsZero() {
		return r.handleTeardown(ctx, &env)
	}

	// Copy CommitSHA from spec to status early
	if env.Spec.Source.CommitSHA != "" && env.Status.CommitSHA == "" {
		env.Status.CommitSHA = env.Spec.Source.CommitSHA
	}

	// Initialize metrics from existing state
	for _, route := range env.Spec.Routing.AsyncRoutes {
		asyncActiveRoutes.WithLabelValues(string(route.Protocol)).Add(0)
	}

	// 3. Add finalizer
	if !controllerutil.ContainsFinalizer(&env, environmentFinalizer) {
		controllerutil.AddFinalizer(&env, environmentFinalizer)
		if err := r.Update(ctx, &env); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		metrics.EnvironmentsActive.Inc()
		for _, route := range env.Spec.Routing.AsyncRoutes {
			asyncActiveRoutes.WithLabelValues(string(route.Protocol)).Inc()
		}
		if r.Notifier != nil {
			tCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := r.Notifier.PostEnvironmentCreated(tCtx, &env); err != nil {
				logger.Error(err, "failed to post environment created notification")
				r.Recorder.Event(&env, "Warning", "NotificationFailed", err.Error())
			}
		}
		if r.StatusReporter != nil {
			notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := r.StatusReporter.PostCommitStatus(notifyCtx, &env, "pending", "Preview environment provisioning"); err != nil {
				logger.Error(err, "failed to post commit status")
			}
		}
		r.Recorder.Event(&env, "Normal", "Created", "Environment created")
		return ctrl.Result{}, nil
	}

	// 4. Set ObservedGeneration
	env.Status.ObservedGeneration = env.Generation

	// 5-7.6 Provisioning (Namespace, DB, Routing, Async, Banner)
	res, done, err := r.reconcileProvisioning(ctx, &env, statusBase)
	if done {
		return res, err
	}

	// 8. Deploy
	res, done, err = r.reconcileDeploy(ctx, &env, statusBase)
	if done {
		return res, err
	}

	// 10. Derive phase
	newPhase := derivePhase(env.Status.Conditions)
	oldPhase := env.Status.Phase
	env.Status.Phase = newPhase

	if env.Status.Phase == divergeiov1alpha1.PhaseRunning {
		r.Recorder.Event(&env, "Normal", "Running", "Environment is up and running")
	}

	// 11. Lifecycle (TTL, Phase transitions, Testing)
	requeueAfter, res, done, err := r.reconcileLifecycle(ctx, &env, statusBase, oldPhase)
	if done {
		return res, err
	}

	// 12. Update status
	res, err = r.updateStatusWithRequeue(ctx, &env, statusBase, nil, requeueAfter)
	if err == nil && oldPhase != newPhase {
		metrics.EnvironmentTransitions.WithLabelValues(
			string(oldPhase), string(newPhase), env.Spec.Source.Provider,
		).Inc()
	}
	return res, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *EnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&divergeiov1alpha1.Environment{}).
		Owns(&batchv1.Job{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
