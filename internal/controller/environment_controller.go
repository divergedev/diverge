package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
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
	"github.com/divergedev/diverge/internal/metrics"
	"github.com/divergedev/diverge/internal/notifier"
	"github.com/divergedev/diverge/internal/routing"
	divtesting "github.com/divergedev/diverge/internal/testing"
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
	StatusReporter   notifier.StatusReporter
	Deployer         deployer.Deployer
	TestRunner       divtesting.TestRunner
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

func (r *EnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	logger := log.FromContext(ctx)

	var env divergeiov1alpha1.Environment
	if err := r.Get(ctx, req.NamespacedName, &env); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Track reconcile outcome — single defer covers all return paths
	defer func() {
		if retErr != nil {
			metrics.ReconcileOutcomes.WithLabelValues("error").Inc()
		} else {
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
		if r.StatusReporter != nil {
			if err := r.StatusReporter.PostCommitStatus(ctx, &env, "pending", "Preview environment provisioning"); err != nil {
				logger.Error(err, "failed to post commit status")
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

	// Note: transition counter is recorded after successful status persist below

	if env.Status.Phase == divergeiov1alpha1.PhaseRunning {
		r.Recorder.Event(&env, "Normal", "Running", "Environment is up and running")
	}

	// 11. Check TTL expiry and set timestamps
	var requeueAfter time.Duration
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
		requeueAfter = time.Until(expiryTime)
	} else if env.Status.CreatedAt == nil {
		now := metav1.Now()
		env.Status.CreatedAt = &now
	}

	if r.Notifier != nil && oldPhase != newPhase {
		switch newPhase {
		case divergeiov1alpha1.PhaseRunning:
			if r.StatusReporter != nil {
				if err := r.StatusReporter.PostCommitStatus(ctx, &env, "success", "Preview environment ready"); err != nil {
					logger.Error(err, "failed to post commit status")
				}
			}
			tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := r.Notifier.PostEnvironmentReady(tCtx, &env); err != nil {
				logger.Error(err, "failed to post environment ready notification")
				r.Recorder.Event(&env, "Warning", "NotificationFailed", err.Error())
			}
		case divergeiov1alpha1.PhaseFailed:
			if r.StatusReporter != nil {
				if err := r.StatusReporter.PostCommitStatus(ctx, &env, "failed", "Preview environment failed"); err != nil {
					logger.Error(err, "failed to post commit status")
				}
			}
			tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := r.Notifier.PostEnvironmentFailed(tCtx, &env, "Environment failed to deploy"); err != nil {
				logger.Error(err, "failed to post environment failed notification")
				r.Recorder.Event(&env, "Warning", "NotificationFailed", err.Error())
			}
		}
	}

	// Trigger tests independently of notifier (CR1: notifier may be noop)
	if oldPhase != newPhase && newPhase == divergeiov1alpha1.PhaseRunning {
		if r.TestRunner != nil && env.Spec.Testing != nil && env.Spec.Testing.Enabled {
			tCtxT, cancelT := context.WithTimeout(ctx, 30*time.Second)
			defer cancelT()
			runID, err := r.TestRunner.Trigger(tCtxT, &env)
			if err != nil {
				logger.Error(err, "failed to trigger tests")
				r.Recorder.Event(&env, "Warning", "TestTriggerFailed", err.Error())
			} else {
				now := metav1.Now()
				env.Status.TestStatus = &divergeiov1alpha1.TestStatus{
					State:     divergeiov1alpha1.TestStatePending,
					RunID:     runID,
					StartedAt: &now,
				}
				r.Recorder.Event(&env, "Normal", "TestsTriggered", "Test run triggered")
				if r.StatusReporter != nil {
					_ = r.StatusReporter.PostCommitStatus(ctx, &env, "pending", "Tests running...")
				}
			}
		}
	}

	// Poll test status if a test run is active
	if r.TestRunner != nil && env.Status.TestStatus != nil {
		ts := env.Status.TestStatus
		// Determine if test failures should block merge
		testRequired := env.Spec.Testing != nil && env.Spec.Testing.Required

		if ts.State == divergeiov1alpha1.TestStatePending || ts.State == divergeiov1alpha1.TestStateRunning {
			// Check for timeout
			testTimeout := 30 * time.Minute // default
			if env.Spec.Testing != nil && env.Spec.Testing.Timeout != nil {
				testTimeout = env.Spec.Testing.Timeout.Duration
			}
			if ts.StartedAt != nil && time.Since(ts.StartedAt.Time) > testTimeout {
				now := metav1.Now()
				ts.State = divergeiov1alpha1.TestStateTimedOut
				ts.Summary = "Tests timed out"
				ts.CompletedAt = &now
				logger.Info("Test run timed out", "runID", ts.RunID)
				r.Recorder.Event(&env, "Warning", "TestsTimedOut", "Test run timed out")
				if r.StatusReporter != nil {
					commitState := "failed"
					if !testRequired {
						commitState = "success" // non-blocking: don't prevent merge
					}
					_ = r.StatusReporter.PostCommitStatus(ctx, &env, commitState, "Tests timed out")
				}
			} else {
				// Poll CI for status
				tCtxP, cancelP := context.WithTimeout(ctx, 15*time.Second)
				defer cancelP()
				result, err := r.TestRunner.Status(tCtxP, &env, ts.RunID)
				if err != nil {
					logger.Error(err, "failed to poll test status", "runID", ts.RunID)
				} else if result != nil {
					ts.State = result.State
					ts.Summary = result.Summary
					if result.URL != "" {
						ts.URL = result.URL
					}

					switch result.State {
					case divergeiov1alpha1.TestStatePassed:
						now := metav1.Now()
						ts.CompletedAt = &now
						r.Recorder.Event(&env, "Normal", "TestsPassed", result.Summary)
						if r.StatusReporter != nil {
							_ = r.StatusReporter.PostCommitStatus(ctx, &env, "success", result.Summary)
						}
					case divergeiov1alpha1.TestStateFailed:
						now := metav1.Now()
						ts.CompletedAt = &now
						r.Recorder.Event(&env, "Warning", "TestsFailed", result.Summary)
						if r.StatusReporter != nil {
							commitState := "failed"
							if !testRequired {
								commitState = "success" // non-blocking
							}
							_ = r.StatusReporter.PostCommitStatus(ctx, &env, commitState, result.Summary)
						}
					}
				}

				// Requeue to poll again if still running
				if ts.State == divergeiov1alpha1.TestStateRunning || ts.State == divergeiov1alpha1.TestStatePending {
					if requeueAfter == 0 || requeueAfter > 30*time.Second {
						requeueAfter = 30 * time.Second
					}
				}
			}
		}
	}

	// 12. Update status (requeueAfter is non-zero when TTL is active)
	res, err := r.updateStatusWithRequeue(ctx, &env, statusBase, nil, requeueAfter)
	if err == nil && oldPhase != newPhase {
		metrics.EnvironmentTransitions.WithLabelValues(
			string(oldPhase), string(newPhase), env.Spec.Source.Provider,
		).Inc()
		// Update active environments gauge: decrement old phase, increment new
		if oldPhase != "" {
			metrics.EnvironmentsActive.WithLabelValues(
				string(oldPhase), env.Spec.Source.Provider,
			).Dec()
		}
		metrics.EnvironmentsActive.WithLabelValues(
			string(newPhase), env.Spec.Source.Provider,
		).Inc()
	}
	return res, err
}

func (r *EnvironmentReconciler) handleTeardown(ctx context.Context, env *divergeiov1alpha1.Environment) (ctrl.Result, error) {
	// Clean up per-environment TTL gauge to prevent cardinality leak
	metrics.EnvironmentTTLRemaining.DeleteLabelValues(env.Name, env.Namespace)

	if controllerutil.ContainsFinalizer(env, environmentFinalizer) {
		r.Recorder.Event(env, "Normal", "Terminating", "Teardown started")

		if r.StatusReporter != nil {
			if err := r.StatusReporter.PostCommitStatus(ctx, env, "canceled", "Preview environment torn down"); err != nil {
				log.FromContext(ctx).Error(err, "failed to post commit status")
			}
		}

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

		// Decrement active environments gauge after successful finalizer
		// removal — this is idempotent because the finalizer is gone,
		// so subsequent reconciles skip this block entirely.
		if env.Status.Phase != "" {
			metrics.EnvironmentsActive.WithLabelValues(
				string(env.Status.Phase), env.Spec.Source.Provider,
			).Dec()
		}

		r.Recorder.Event(env, "Normal", "Terminated", "Teardown complete")
	}
	return ctrl.Result{}, nil
}

func (r *EnvironmentReconciler) ensureNamespace(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	if env.Spec.Deploy.Namespace == "create" {
		labels := map[string]string{
			"diverge.io/environment": env.Name,
			"diverge.io/managed-by":  "diverge",
		}
		// Merge user-defined labels; diverge.io/* labels take precedence
		for k, v := range env.Spec.Deploy.NamespaceLabels {
			if !strings.HasPrefix(k, "diverge.io/") {
				labels[k] = v
			}
		}

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: env.PreviewNamespace(),
			},
		}
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ns, func() error {
			ns.Labels = labels
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to create or update namespace: %w", err)
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
		Owns(&batchv1.Job{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
