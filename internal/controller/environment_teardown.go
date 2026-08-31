package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/metrics"
)

func (r *EnvironmentReconciler) handleTeardown(ctx context.Context, env *divergeiov1alpha1.Environment) (ctrl.Result, error) {
	// Clean up per-environment TTL gauge to prevent cardinality leak
	metrics.EnvironmentTTLRemaining.DeleteLabelValues(env.Name, env.Namespace)

	if controllerutil.ContainsFinalizer(env, environmentFinalizer) {
		r.Recorder.Event(env, "Normal", "Terminating", "Teardown started")

		if r.StatusReporter != nil {
			notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := r.StatusReporter.PostCommitStatus(notifyCtx, env, "canceled", "Preview environment torn down"); err != nil {
				log.FromContext(ctx).Error(err, "failed to post commit status")
			}
		}

		if r.Notifier != nil {
			tCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := r.Notifier.PostEnvironmentTeardown(tCtx, env); err != nil {
				log.FromContext(ctx).Error(err, "failed to post environment teardown notification")
				r.Recorder.Event(env, "Warning", "NotificationFailed", err.Error())
			}
		}

		var errs []error

		if r.Deployer != nil {
			tCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			if err := r.Deployer.Teardown(tCtx, env); err != nil {
				errs = append(errs, fmt.Errorf("failed to teardown deployments: %w", err))
			}
			cancel()
		}

		// Teardown async routes
		if len(env.Spec.Routing.AsyncRoutes) > 0 && r.AsyncProvisioner != nil {
			for _, route := range env.Spec.Routing.AsyncRoutes {
				tCtxA, cancelA := context.WithTimeout(ctx, 15*time.Second)
				if err := r.AsyncProvisioner.Teardown(tCtxA, env, route); err != nil {
					errs = append(errs, fmt.Errorf("failed to teardown async route %s/%s: %w", route.Protocol, route.Target, err))
					asyncTeardownsTotal.WithLabelValues(string(route.Protocol), "error").Inc()
				} else {
					asyncTeardownsTotal.WithLabelValues(string(route.Protocol), "success").Inc()
				}
				cancelA()
			}
		}

		tCtxR, cancelR := context.WithTimeout(ctx, 15*time.Second)
		if err := r.Router.Teardown(tCtxR, env); err != nil {
			errs = append(errs, fmt.Errorf("failed to teardown routing: %w", err))
		}
		cancelR()

		tCtxDB, cancelDB := context.WithTimeout(ctx, 15*time.Second)
		if err := r.DatabaseProvider.Teardown(tCtxDB, env); err != nil {
			errs = append(errs, fmt.Errorf("failed to teardown database: %w", err))
		}
		cancelDB()

		if len(errs) > 0 {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, errors.Join(errs...)
		}

		// C4: Wait for ArgoCD Applications to be fully deleted before
		// deleting the namespace, preventing finalizer deadlocks where
		// the namespace enters Terminating but ArgoCD resources still
		// have resources-finalizer.argocd.argoproj.io.
		if env.Spec.Deploy.Namespace == "create" {
			if r.Deployer != nil {
				tCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
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
			deleteCtx, cancelDelete := context.WithTimeout(ctx, 15*time.Second)
			err := r.Delete(deleteCtx, ns)
			cancelDelete()
			if err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to delete namespace: %w", err)
			}
		}

		controllerutil.RemoveFinalizer(env, environmentFinalizer)
		updateCtx, cancelUpdate := context.WithTimeout(ctx, 15*time.Second)
		err := r.Update(updateCtx, env)
		cancelUpdate()
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
		}

		metrics.EnvironmentsActive.Dec()
		for _, route := range env.Spec.Routing.AsyncRoutes {
			asyncActiveRoutes.WithLabelValues(string(route.Protocol)).Dec()
		}

		r.Recorder.Event(env, "Normal", "Terminated", "Teardown complete")
	}
	return ctrl.Result{}, nil
}
