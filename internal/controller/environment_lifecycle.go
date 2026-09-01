package controller

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func (r *EnvironmentReconciler) reconcileLifecycle(ctx context.Context, env *divergeiov1alpha1.Environment, statusBase *divergeiov1alpha1.Environment, oldPhase divergeiov1alpha1.EnvironmentPhase) (time.Duration, ctrl.Result, bool, error) {
	ctx, span := otel.Tracer("diverge").Start(ctx, "reconcileLifecycle")
	defer span.End()

	logger := log.FromContext(ctx)
	newPhase := env.Status.Phase

	// 11. Check TTL expiry and set timestamps
	var requeueAfter time.Duration
	if env.Status.CreatedAt == nil {
		now := metav1.Now()
		env.Status.CreatedAt = &now
	}

	if env.Spec.Lifecycle.TTL != nil {
		expiryTime := env.Status.CreatedAt.Add(env.Spec.Lifecycle.TTL.Duration)
		env.Status.ExpiresAt = &metav1.Time{Time: expiryTime}
		if time.Now().After(expiryTime) {
			logger.Info("Environment TTL expired, triggering deletion")
			deleteCtx, cancelDelete := context.WithTimeout(ctx, 15*time.Second)
			err := r.Delete(deleteCtx, env)
			cancelDelete()
			if err != nil {
				return 0, ctrl.Result{}, true, fmt.Errorf("failed to delete expired environment: %w", err)
			}
			return 0, ctrl.Result{}, true, nil
		}
		// C1: Requeue when TTL expires so the controller wakes up to delete
		requeueAfter = time.Until(expiryTime)
	}

	if r.Notifier != nil && oldPhase != newPhase {
		switch newPhase {
		case divergeiov1alpha1.PhaseRunning:
			if r.StatusReporter != nil {
				notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				if err := r.StatusReporter.PostCommitStatus(notifyCtx, env, "success", "Preview environment ready"); err != nil {
					logger.Error(err, "failed to post commit status")
				}
			}
			tCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := r.Notifier.PostEnvironmentReady(tCtx, env); err != nil {
				logger.Error(err, "failed to post environment ready notification")
				r.Recorder.Event(env, "Warning", "NotificationFailed", err.Error())
			}
		case divergeiov1alpha1.PhaseFailed:
			if r.StatusReporter != nil {
				notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				if err := r.StatusReporter.PostCommitStatus(notifyCtx, env, "failed", "Preview environment failed"); err != nil {
					logger.Error(err, "failed to post commit status")
				}
			}
			tCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := r.Notifier.PostEnvironmentFailed(tCtx, env, "Environment failed to deploy"); err != nil {
				logger.Error(err, "failed to post environment failed notification")
				r.Recorder.Event(env, "Warning", "NotificationFailed", err.Error())
			}
		}
	}

	// Trigger tests independently of notifier (CR1: notifier may be noop)
	if oldPhase != newPhase && newPhase == divergeiov1alpha1.PhaseRunning {
		if r.TestRunner != nil && env.Spec.Testing != nil && env.Spec.Testing.Enabled {
			tCtxT, cancelT := context.WithTimeout(ctx, 15*time.Second)
			defer cancelT()
			runID, err := r.TestRunner.Trigger(tCtxT, env)
			if err != nil {
				logger.Error(err, "failed to trigger tests")
				r.Recorder.Event(env, "Warning", "TestTriggerFailed", err.Error())
				now := metav1.Now()
				env.Status.TestStatus = &divergeiov1alpha1.TestStatus{
					State:       divergeiov1alpha1.TestStateFailed,
					Summary:     fmt.Sprintf("Failed to trigger: %v", err),
					CompletedAt: &now,
				}
				if r.StatusReporter != nil {
					commitState := "failed"
					if env.Spec.Testing == nil || !env.Spec.Testing.Required {
						commitState = "success"
					}
					notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
					_ = r.StatusReporter.PostCommitStatus(notifyCtx, env, commitState, err.Error())
					cancel()
				}
			} else {
				now := metav1.Now()
				env.Status.TestStatus = &divergeiov1alpha1.TestStatus{
					State:     divergeiov1alpha1.TestStatePending,
					RunID:     runID,
					StartedAt: &now,
				}
				r.Recorder.Event(env, "Normal", "TestsTriggered", "Test run triggered")
				if r.StatusReporter != nil {
					notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
					_ = r.StatusReporter.PostCommitStatus(notifyCtx, env, "pending", "Tests running...")
					cancel()
				}
				if runID == "dispatch-pending" {
					res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, nil, 5*time.Second)
					return requeueAfter, res, true, retErr
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
				r.Recorder.Event(env, "Warning", "TestsTimedOut", "Test run timed out")
				if r.StatusReporter != nil {
					commitState := "failed"
					if !testRequired {
						commitState = "success" // non-blocking: don't prevent merge
					}
					notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
					_ = r.StatusReporter.PostCommitStatus(notifyCtx, env, commitState, "Tests timed out")
					cancel()
				}
			} else {
				// Poll CI for status
				tCtxP, cancelP := context.WithTimeout(ctx, 15*time.Second)
				defer cancelP()
				result, err := r.TestRunner.Status(tCtxP, env, ts.RunID)
				if err != nil {
					logger.Error(err, "failed to poll test status", "runID", ts.RunID)
				} else if result != nil {
					ts.State = result.State
					ts.Summary = result.Summary
					if result.URL != "" {
						ts.URL = result.URL
					}
					if result.ResolvedRunID != "" {
						ts.RunID = result.ResolvedRunID
					}

					switch result.State {
					case divergeiov1alpha1.TestStatePassed:
						now := metav1.Now()
						ts.CompletedAt = &now
						r.Recorder.Event(env, "Normal", "TestsPassed", result.Summary)
						if r.StatusReporter != nil {
							notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
							_ = r.StatusReporter.PostCommitStatus(notifyCtx, env, "success", result.Summary)
							cancel()
						}
					case divergeiov1alpha1.TestStateFailed:
						now := metav1.Now()
						ts.CompletedAt = &now
						r.Recorder.Event(env, "Warning", "TestsFailed", result.Summary)
						if r.StatusReporter != nil {
							commitState := "failed"
							if !testRequired {
								commitState = "success" // non-blocking
							}
							notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
							_ = r.StatusReporter.PostCommitStatus(notifyCtx, env, commitState, result.Summary)
							cancel()
						}
					}
				}

				// Requeue to poll again if still running
				switch ts.State {
				case divergeiov1alpha1.TestStatePending:
					if ts.StartedAt == nil {
						requeueAfter = 5 * time.Second
					} else {
						elapsed := time.Since(ts.StartedAt.Time)
						if elapsed < 30*time.Second {
							requeueAfter = 5 * time.Second
						} else if elapsed < 60*time.Second {
							requeueAfter = 10 * time.Second
						} else {
							requeueAfter = 30 * time.Second
						}
					}
				case divergeiov1alpha1.TestStateRunning:
					if requeueAfter == 0 || requeueAfter > 15*time.Second {
						requeueAfter = 30 * time.Second
					}
				}
			}
		}
	}

	return requeueAfter, ctrl.Result{}, false, nil
}
