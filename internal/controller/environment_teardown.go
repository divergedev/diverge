package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"

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
	ctx, span := otel.Tracer("diverge").Start(ctx, "handleTeardown")
	defer span.End()

	// Clean up per-environment TTL and cost gauges to prevent cardinality leak
	metrics.EnvironmentTTLRemaining.DeleteLabelValues(env.Name, env.Namespace)
	metrics.DeleteEnvironmentCostMetric(env.Name, env.Namespace)

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
				prov := r.resolveAsyncProvisioner(route)
				if err := prov.Teardown(tCtxA, env, route); err != nil {
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

		if dbProv, _, dbResolveErr := r.resolveDatabaseProvider(env, true); dbResolveErr != nil {
			errs = append(errs, fmt.Errorf("failed to resolve database provider for teardown: %w", dbResolveErr))
		} else if dbProv != nil {
			if dbProv != r.DatabaseProvider {
				defer closeDatabaseProvider(dbProv)
			}
			tCtxDB, cancelDB := context.WithTimeout(ctx, 15*time.Second)
			if err := dbProv.Teardown(tCtxDB, env); err != nil {
				errs = append(errs, fmt.Errorf("failed to teardown database: %w", err))
			}
			cancelDB()
		}

		// Wait for preview workloads to be fully terminated before deleting
		// feature flag ConfigMaps or namespaces, preventing live preview pods
		// from encountering volume or ConfigMap missing errors during shutdown.
		if r.Deployer != nil {
			tCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			status, err := r.Deployer.Status(tCtx, env)
			cancel()
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to check deployer status during teardown: %w", err))
			} else if len(status) > 0 {
				log.FromContext(ctx).Info("Waiting for deployer resources to be fully deleted", "remaining", len(status))
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}
		}

		// Delete auto-provisioned database env Secret (after workloads have terminated)
		targetNS := env.Namespace
		if env.Spec.Deploy.Namespace == "create" {
			targetNS = env.PreviewNamespace()
		}
		dbSecretName := sanitizeK8sName(fmt.Sprintf("diverge-db-env-%s", env.Name), env.Name, 63)
		dbSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dbSecretName,
				Namespace: targetNS,
			},
		}
		delSecCtx, cancelDelSec := context.WithTimeout(ctx, 15*time.Second)
		if err := r.Delete(delSecCtx, dbSecret); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to delete database env secret: %w", err))
		}
		cancelDelSec()

		// Teardown features (executed only after deployer workloads have terminated)
		if fp, err := r.getFeatureProvider(env); err == nil && fp != nil {
			tCtxF, cancelF := context.WithTimeout(ctx, 15*time.Second)
			if err := fp.Teardown(tCtxF, env); err != nil {
				errs = append(errs, fmt.Errorf("failed to teardown features: %w", err))
			}
			cancelF()
		}

		// C4: Wait for workloads to be fully deleted before deleting the namespace,
		// preventing finalizer deadlocks where the namespace enters Terminating
		// but resources still retain finalizers.
		if env.Spec.Deploy.Namespace == "create" {
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: env.PreviewNamespace(),
				},
			}
			deleteCtx, cancelDelete := context.WithTimeout(ctx, 15*time.Second)
			err := r.Delete(deleteCtx, ns)
			cancelDelete()
			if err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete namespace: %w", err))
			}
		}

		if len(errs) > 0 && (env.Annotations["divergedev.com/force-teardown"] == "true" || (!env.DeletionTimestamp.IsZero() && time.Since(env.DeletionTimestamp.Time) > 15*time.Minute)) {
			log.FromContext(ctx).Info("Force-teardown threshold or annotation reached; bypassing teardown errors to release finalizer", "errors", errors.Join(errs...))
			if r.Recorder != nil {
				r.Recorder.Event(env, "Warning", "ForceTeardown", "Bypassed external teardown errors after timeout or force-teardown annotation")
			}
			errs = nil
		}

		if len(errs) > 0 {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, errors.Join(errs...)
		}

		controllerutil.RemoveFinalizer(env, environmentFinalizer)
		updateCtx, cancelUpdate := context.WithTimeout(ctx, 15*time.Second)
		err := r.Update(updateCtx, env)
		cancelUpdate()
		if err != nil {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf("failed to remove finalizer: %w", err)
		}

		metrics.EnvironmentsActive.Dec()
		for _, route := range env.Spec.Routing.AsyncRoutes {
			asyncActiveRoutes.WithLabelValues(string(route.Protocol)).Dec()
		}

		r.Recorder.Event(env, "Normal", "Terminated", "Teardown complete")
	}
	return ctrl.Result{}, nil
}
