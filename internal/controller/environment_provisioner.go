package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"golang.org/x/sync/errgroup"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/config/banner"
	"github.com/divergedev/diverge/internal/async"
	internaldb "github.com/divergedev/diverge/internal/database"
	"github.com/divergedev/diverge/internal/multicluster"
	pkgdb "github.com/divergedev/diverge/pkg/database"
	"github.com/divergedev/diverge/pkg/features"
	"github.com/divergedev/diverge/pkg/registry"
)

const annotationSharedGroupDB = "divergedev.com/shared-group-db"

func isPreviewGroupChildEnvironment(env *divergeiov1alpha1.Environment) bool {
	return env != nil && env.Labels != nil && env.Labels[labelManagedBy] == "diverge-previewgroup"
}

func (r *EnvironmentReconciler) notifyFailed(ctx context.Context, env *divergeiov1alpha1.Environment, msg string) {
	if r.Notifier == nil || isPreviewGroupChildEnvironment(env) {
		return
	}
	tCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := r.Notifier.PostEnvironmentFailed(tCtx, env, msg); err != nil {
		log.FromContext(ctx).Error(err, "failed to post environment failed notification")
		r.Recorder.Event(env, "Warning", "NotificationFailed", err.Error())
	}
}

func (r *EnvironmentReconciler) reconcileProvisioning(ctx context.Context, env *divergeiov1alpha1.Environment, statusBase *divergeiov1alpha1.Environment) (ctrl.Result, bool, error) {
	_, res, done, err := r.reconcileProvisioningWithTarget(ctx, env, statusBase)
	return res, done, err
}

func (r *EnvironmentReconciler) reconcileProvisioningWithTarget(ctx context.Context, env *divergeiov1alpha1.Environment, statusBase *divergeiov1alpha1.Environment) (context.Context, ctrl.Result, bool, error) {
	pCtx, span := otel.Tracer("diverge").Start(ctx, "reconcileProvisioning")
	defer span.End()
	ctx = pCtx

	logger := log.FromContext(ctx)

	// S5: Cross-namespace SecretRef Validation
	for _, ref := range env.Spec.EnvFrom {
		if ref.Namespace != "" && ref.Namespace != env.Namespace {
			r.Recorder.Event(env, "Warning", "SecurityViolation",
				fmt.Sprintf("cross-namespace SecretRef not allowed: %s/%s", ref.Namespace, ref.Name))
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    "SecretRefValid",
				Status:  metav1.ConditionFalse,
				Reason:  "CrossNamespaceRef",
				Message: "cross-namespace SecretRef not allowed",
			})
			res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, nil, 0)
			return ctx, res, true, retErr
		}
	}
	for _, ref := range env.Spec.Deploy.EnvFrom {
		if ref.Namespace != "" && ref.Namespace != env.Namespace {
			r.Recorder.Event(env, "Warning", "SecurityViolation",
				fmt.Sprintf("cross-namespace SecretRef not allowed: %s/%s", ref.Namespace, ref.Name))
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    "SecretRefValid",
				Status:  metav1.ConditionFalse,
				Reason:  "CrossNamespaceRef",
				Message: "cross-namespace SecretRef not allowed",
			})
			res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, nil, 0)
			return ctx, res, true, retErr
		}
	}

	// 4.5. Resolve target cluster once (local hub or remote spoke via clusterRef) and pin for the reconcile
	if prevCluster := strings.TrimSpace(env.Status.TargetCluster); prevCluster != "" {
		desiredCluster := multicluster.LocalClusterName
		if multicluster.IsRemoteEnv(env) {
			desiredCluster = multicluster.DesiredClusterName(env.Spec.Deploy.ClusterRef)
		}
		if prevCluster != desiredCluster {
			msg := fmt.Sprintf("spec.deploy.clusterRef cannot change target cluster from %q to %q after provisioning", prevCluster, desiredCluster)
			r.Recorder.Event(env, "Warning", "ClusterRefImmutable", msg)
			env.Status.Phase = divergeiov1alpha1.PhaseFailed
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    "SecretRefValid",
				Status:  metav1.ConditionFalse,
				Reason:  "ClusterRefImmutable",
				Message: msg,
			})
			res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, nil, 0)
			return ctx, res, true, retErr
		}
	}

	resolvedTarget, clusterErr := r.resolveTarget(ctx, env)
	if clusterErr != nil {
		if ve, ok := multicluster.IsValidationError(clusterErr); ok {
			if ve.Reason == "CrossNamespaceRef" {
				r.Recorder.Event(env, "Warning", "SecurityViolation", ve.Message)
			} else {
				r.Recorder.Event(env, "Warning", "ClusterRefInvalid", ve.Message)
				env.Status.Phase = divergeiov1alpha1.PhaseFailed
			}
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    "SecretRefValid",
				Status:  metav1.ConditionFalse,
				Reason:  ve.Reason,
				Message: ve.Message,
			})
			res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, nil, 0)
			return ctx, res, true, retErr
		}
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "NamespaceReady",
			Status:  metav1.ConditionFalse,
			Reason:  "ClusterResolveFailed",
			Message: clusterErr.Error(),
		})
		res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, clusterErr, 0)
		return ctx, res, true, retErr
	}
	ctx = multicluster.WithTarget(ctx, resolvedTarget)
	env.Status.TargetCluster = resolvedTarget.Name
	if meta.FindStatusCondition(env.Status.Conditions, "SecretRefValid") != nil ||
		len(env.Spec.EnvFrom) > 0 ||
		len(env.Spec.Deploy.EnvFrom) > 0 ||
		env.Spec.Deploy.ClusterRef != nil {
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "SecretRefValid",
			Status:  metav1.ConditionTrue,
			Reason:  "SecretRefsValid",
			Message: "All secret references are valid",
		})
	}
	// Write-ahead persist TargetCluster with an optimistic lock before creating any remote spoke resources so
	// concurrent spec updates conflict before spoke resources are provisioned and teardown always knows the target.
	if resolvedTarget.IsRemote && strings.TrimSpace(statusBase.Status.TargetCluster) == "" {
		if patchErr := r.Status().Patch(ctx, env, client.MergeFromWithOptions(statusBase, client.MergeFromWithOptimisticLock{})); patchErr != nil {
			return ctx, ctrl.Result{}, true, fmt.Errorf("failed to persist targetCluster status: %w", patchErr)
		}
		*statusBase = *env.DeepCopy()
	}

	// 5. Ensure namespace
	if err := r.ensureNamespace(ctx, env); err != nil {
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "NamespaceReady",
			Status:  metav1.ConditionFalse,
			Reason:  "NamespaceProvisionFailed",
			Message: err.Error(),
		})
		r.notifyFailed(ctx, env, err.Error())
		res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, err, 0)
		return ctx, res, true, retErr
	}
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:    "NamespaceReady",
		Status:  metav1.ConditionTrue,
		Reason:  "NamespaceProvisioned",
		Message: "Namespace is ready",
	})

	// 5.5. Enforce namespace quotas and FinOps LRU auto-hibernation
	if res, done, err := r.reconcileNamespaceQuota(ctx, env, statusBase); done || err != nil {
		if done {
			return ctx, res, true, err
		}
		res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, err, 0)
		return ctx, res, true, retErr
	}

	// 6. Ensure database
	var dbStatus *pkgdb.DatabaseResult
	var resolvedDBName string
	if env.Annotations != nil && env.Annotations[annotationSharedGroupDB] == "true" {
		// Database lifecycle and diverge-db-env-<env> Secret are owned by the parent PreviewGroup.
		sharedSecretName, sharedDSN, sharedReady, sharedErr := r.readSharedGroupDatabaseDSN(ctx, env)
		if sharedErr != nil {
			env.Status.Phase = divergeiov1alpha1.PhaseFailed
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    "DatabaseReady",
				Status:  metav1.ConditionFalse,
				Reason:  "DatabaseProvisionFailed",
				Message: sharedErr.Error(),
			})
			res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, sharedErr, 0)
			return ctx, res, true, retErr
		}
		if !sharedReady {
			env.Status.Phase = divergeiov1alpha1.PhaseDeploying
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    "DatabaseReady",
				Status:  metav1.ConditionFalse,
				Reason:  "WaitingForSharedDatabaseSecret",
				Message: "Waiting for parent PreviewGroup shared database Secret",
			})
			res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, nil, 3*time.Second)
			return ctx, res, true, retErr
		}
		// Retain env.Status.DatabaseProvider so handleTeardown still releases any prior standalone database provisioned before adoption.
		if sharedSecretName != "" {
			ctx = internaldb.WithResolvedSharedDBSecret(ctx, env.Namespace, env.Name, sharedSecretName)
		}
		dbStatus = &pkgdb.DatabaseResult{
			DSN:              sharedDSN,
			Ready:            true,
			SetupSQLExecuted: true,
			Message:          "Using shared PreviewGroup database",
		}
	} else {
		tCtxDB, cancelDB := context.WithTimeout(ctx, 15*time.Second)
		defer cancelDB()
		var dbProv pkgdb.DatabaseProvider
		var err error
		dbProv, resolvedDBName, err = r.resolveDatabaseProviderWithContext(tCtxDB, env, false)
		if dbProv != nil && dbProv != r.DatabaseProvider {
			defer closeDatabaseProvider(dbProv)
		}
		if err == nil && dbProv != nil {
			if env.Status.DatabaseProvider == "" && resolvedDBName != "" {
				env.Status.DatabaseProvider = resolvedDBName
			}
			dbStatus, err = dbProv.Provision(tCtxDB, env)
		}
		if err != nil {
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    "DatabaseReady",
				Status:  metav1.ConditionFalse,
				Reason:  "DatabaseProvisionFailed",
				Message: err.Error(),
			})
			r.notifyFailed(ctx, env, err.Error())
			res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, err, 0)
			return ctx, res, true, retErr
		}
	}
	if dbStatus != nil && dbStatus.Ready {
		// Run SetupSQL if configured and not already executed in-process by the provider
		if dbStatus.SetupSQL != "" && !dbStatus.SetupSQLExecuted {
			runner := r.SetupRunner
			if runner == nil {
				runner = &DirectSetupRunner{}
			}
			if setupErr := runner.RunSetupSQL(ctx, env, dbStatus.SetupSQL, dbStatus.AdminDSN); setupErr != nil {
				if errors.Is(setupErr, ErrHookInProgress) {
					meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
						Type:    "DatabaseReady",
						Status:  metav1.ConditionFalse,
						Reason:  "DatabaseSetupRunning",
						Message: "SetupSQL job is running",
					})
					res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, nil, 2*time.Second)
					return ctx, res, true, retErr
				}
				meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
					Type:    "DatabaseReady",
					Status:  metav1.ConditionFalse,
					Reason:  "DatabaseSetupFailed",
					Message: setupErr.Error(),
				})
				r.notifyFailed(ctx, env, setupErr.Error())
				res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, setupErr, 0)
				return ctx, res, true, retErr
			}
		}

		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "DatabaseReady",
			Status:  metav1.ConditionTrue,
			Reason:  "DatabaseProvisioned",
			Message: "Database is ready",
		})
		env.Status.DatabaseStatus = dbStatus.Message

		isBranchingMode := env.Spec.Database.Mode == "snapshot" ||
			env.Spec.Database.Mode == "cnpg" ||
			env.Spec.Database.Mode == "fresh" ||
			env.Spec.Database.Mode == "neon" ||
			resolvedDBName == "neon" ||
			strings.TrimSpace(env.Status.DatabaseProvider) == "neon" ||
			env.Annotations["divergedev.com/database-provider"] != ""
		connRef := strings.TrimSpace(env.Spec.Database.ConnectionRef)
		if env.Annotations[annotationSharedGroupDB] != "true" &&
			(connRef == "" || connRef == internaldb.EnvSecretName(env.Name) || isBranchingMode) &&
			(len(dbStatus.EnvVars) > 0 || env.Spec.Database.Mode == "schema") {
			envVars := dbStatus.EnvVars
			if len(envVars) == 0 {
				envVars = map[string]string{
					"DATABASE_URL": dbStatus.DSN,
				}
			}
			if err := r.ensureDatabaseEnvSecret(ctx, env, envVars); err != nil {
				meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
					Type:    "DatabaseReady",
					Status:  metav1.ConditionFalse,
					Reason:  "DatabaseSecretProvisionFailed",
					Message: err.Error(),
				})
				r.notifyFailed(ctx, env, err.Error())
				res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, err, 0)
				return ctx, res, true, retErr
			}
		}

		// Run migrations (Atlas / MigrationJob) if configured
		if env.Spec.Database.Atlas != nil || env.Spec.Database.MigrationJob != nil {
			isBlocking := true
			if env.Spec.Database.Atlas != nil && env.Spec.Database.Atlas.Blocking != nil {
				isBlocking = *env.Spec.Database.Atlas.Blocking
			} else if env.Spec.Database.MigrationJob != nil && env.Spec.Database.MigrationJob.Blocking != nil {
				isBlocking = *env.Spec.Database.MigrationJob.Blocking
			}

			migCtx, migCancel := context.WithTimeout(ctx, 60*time.Second)
			defer migCancel()
			migErr := r.runMigrations(migCtx, env, dbStatus)
			if errors.Is(migErr, ErrHookInProgress) {
				env.Status.MigrationStatus = "Running"
				env.Status.MigrationMessage = migErr.Error()
				meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
					Type:    "MigrationReady",
					Status:  metav1.ConditionFalse,
					Reason:  "MigrationRunning",
					Message: migErr.Error(),
				})
				if isBlocking {
					env.Status.Phase = divergeiov1alpha1.PhaseMigrating
					r.Recorder.Event(env, "Normal", "MigrationInProgress", migErr.Error())
					res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, nil, 3*time.Second)
					return ctx, res, true, retErr
				}
			} else if errors.Is(migErr, ErrHookFailed) {
				env.Status.MigrationStatus = "Failed"
				env.Status.MigrationMessage = migErr.Error()
				meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
					Type:    "MigrationReady",
					Status:  metav1.ConditionFalse,
					Reason:  "MigrationFailed",
					Message: migErr.Error(),
				})
				if isBlocking {
					env.Status.Phase = divergeiov1alpha1.PhaseFailed
					r.Recorder.Event(env, "Warning", "MigrationFailed", migErr.Error())
					r.notifyFailed(ctx, env, migErr.Error())
					res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, migErr, 0)
					return ctx, res, true, retErr
				}
			} else if migErr != nil {
				logger.Error(migErr, "unexpected migration error")
				env.Status.MigrationStatus = "Failed"
				env.Status.MigrationMessage = migErr.Error()
				meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
					Type:    "MigrationReady",
					Status:  metav1.ConditionFalse,
					Reason:  "MigrationError",
					Message: migErr.Error(),
				})
				if isBlocking {
					env.Status.Phase = divergeiov1alpha1.PhaseFailed
					r.Recorder.Event(env, "Warning", "MigrationFailed", migErr.Error())
					r.notifyFailed(ctx, env, migErr.Error())
					res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, migErr, 0)
					return ctx, res, true, retErr
				}
			} else {
				env.Status.MigrationStatus = "Succeeded"
				env.Status.MigrationMessage = "Migrations applied successfully"
				meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
					Type:    "MigrationReady",
					Status:  metav1.ConditionTrue,
					Reason:  "MigrationSucceeded",
					Message: "Migrations applied successfully",
				})
			}
		}
	} else {
		provMsg := "Database is provisioning"
		if dbStatus != nil && strings.TrimSpace(dbStatus.Message) != "" {
			provMsg = dbStatus.Message
		}
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "DatabaseReady",
			Status:  metav1.ConditionFalse,
			Reason:  "DatabaseProvisioning",
			Message: provMsg,
		})
		if isNeonDBProvider(resolvedDBName, r.DatabaseProvider, env) {
			res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, nil, 3*time.Second)
			return ctx, res, true, retErr
		}
	}

	// 7. Ensure routing
	tCtxR, cancelR := context.WithTimeout(ctx, 15*time.Second)
	defer cancelR()
	if err := r.Router.Reconcile(tCtxR, env); err != nil {
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "RoutingReady",
			Status:  metav1.ConditionFalse,
			Reason:  "RoutingProvisionFailed",
			Message: err.Error(),
		})
		r.notifyFailed(ctx, env, err.Error())
		res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, err, 0)
		return ctx, res, true, retErr
	}
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:    "RoutingReady",
		Status:  metav1.ConditionTrue,
		Reason:  "RoutingProvisioned",
		Message: "Routing is ready",
	})
	env.Status.URL = r.Router.GetExternalURL(env)

	// 7.5. Ensure async routing
	if len(env.Spec.Routing.AsyncRoutes) > 0 && r.AsyncProvisioner != nil {
		if meta.IsStatusConditionTrue(env.Status.Conditions, "AsyncRoutingReady") {
			// Skip re-provisioning
		} else {
			for _, route := range env.Spec.Routing.AsyncRoutes {
				if err := validateEnvVarMapping(route.EnvVarMapping); err != nil {
					meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
						Type:    "AsyncRoutingReady",
						Status:  metav1.ConditionFalse,
						Reason:  "EnvVarConflict",
						Message: err.Error(),
					})
					r.Recorder.Event(env, "Warning", "ValidationFailed", err.Error())
					res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, nil, 0)
					return ctx, res, true, retErr
				}
			}

			asyncEnvVars := make(map[string]string)
			type routeResult struct {
				route    divergeiov1alpha1.AsyncRouteSpec
				envVars  map[string]string
				duration float64
			}

			g, gCtx := errgroup.WithContext(ctx)
			g.SetLimit(5) // bounded concurrency
			results := make([]routeResult, len(env.Spec.Routing.AsyncRoutes))

			envCopy := env.DeepCopy()
			for i, route := range env.Spec.Routing.AsyncRoutes {
				i, route := i, route
				g.Go(func() error {
					tCtxA, cancelA := context.WithTimeout(gCtx, 30*time.Second)
					defer cancelA()
					startA := time.Now()
					prov := r.resolveAsyncProvisioner(route)
					result, err := prov.Provision(tCtxA, envCopy, route)
					durationA := time.Since(startA).Seconds()
					if err == nil && result == nil {
						err = async.ErrNilProvisionResult
					}
					if err != nil {
						asyncProvisionDuration.WithLabelValues(string(route.Protocol)).Observe(durationA)
						asyncProvisionsTotal.WithLabelValues(string(route.Protocol), "error").Inc()
						return fmt.Errorf("async provision failed for %s/%s: %w", route.Protocol, route.Target, err)
					}
					asyncProvisionDuration.WithLabelValues(string(route.Protocol)).Observe(durationA)
					asyncProvisionsTotal.WithLabelValues(string(route.Protocol), "success").Inc()
					results[i] = routeResult{route: route, envVars: result.EnvVars, duration: durationA}
					return nil
				})
			}

			if err := g.Wait(); err != nil {
				meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
					Type:    "AsyncRoutingReady",
					Status:  metav1.ConditionFalse,
					Reason:  "AsyncProvisionFailed",
					Message: err.Error(),
				})
				r.Recorder.Event(env, "Warning", "AsyncProvisionFailed", err.Error())
				res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, err, 0)
				return ctx, res, true, retErr
			}

			// Merge env vars sequentially to detect collisions deterministically
			for _, rr := range results {
				keys := make([]string, 0, len(rr.envVars))
				for k := range rr.envVars {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					v := rr.envVars[k]
					if existing, ok := asyncEnvVars[k]; ok && existing != v {
						err := fmt.Errorf("env var collision for %q", k)
						meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
							Type:    "AsyncRoutingReady",
							Status:  metav1.ConditionFalse,
							Reason:  "EnvVarConflict",
							Message: err.Error(),
						})
						r.Recorder.Event(env, "Warning", "ValidationFailed", err.Error())
						res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, nil, 0)
						return ctx, res, true, retErr
					}
					asyncEnvVars[k] = v
				}
			}
			env.Status.AsyncEnvVars = asyncEnvVars
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    "AsyncRoutingReady",
				Status:  metav1.ConditionTrue,
				Reason:  "AsyncProvisioned",
				Message: fmt.Sprintf("%d async routes provisioned", len(env.Spec.Routing.AsyncRoutes)),
			})
		}
	}

	// 7.6. Ensure banner ConfigMap
	if env.Spec.Routing.Banner != nil && env.Spec.Routing.Banner.Enabled {
		if err := r.ensureBannerConfigMap(ctx, env); err != nil {
			logger.Error(err, "failed to provision banner ConfigMap")
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    "BannerReady",
				Status:  metav1.ConditionFalse,
				Reason:  "BannerProvisionFailed",
				Message: err.Error(),
			})
		} else {
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    "BannerReady",
				Status:  metav1.ConditionTrue,
				Reason:  "BannerProvisioned",
				Message: "Preview banner ConfigMap is ready",
			})
		}
	} else {
		if err := r.ensureBannerConfigMap(ctx, env); err != nil {
			logger.Error(err, "failed to teardown banner ConfigMap")
		}
	}

	// 7.7. Ensure features
	if env.Spec.Features != nil {
		if meta.IsStatusConditionTrue(env.Status.Conditions, "FeaturesReady") && (env.Status.FeatureConfigMap != "" || len(env.Status.FeatureEnvVars) > 0 || len(env.Spec.Features.Overrides) == 0) {
			// Skip re-provisioning if already ready and status recorded
		} else {
			fp, err := r.getFeatureProvider(ctx, env)
			if err != nil {
				meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
					Type:    "FeaturesReady",
					Status:  metav1.ConditionFalse,
					Reason:  "FeatureProviderNotFound",
					Message: err.Error(),
				})
				r.Recorder.Event(env, "Warning", "FeatureProvisionFailed", err.Error())
				res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, err, 0)
				return ctx, res, true, retErr
			}

			tCtxF, cancelF := context.WithTimeout(ctx, 15*time.Second)
			featResult, err := fp.Provision(tCtxF, env)
			cancelF()
			if err != nil {
				meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
					Type:    "FeaturesReady",
					Status:  metav1.ConditionFalse,
					Reason:  "FeatureProvisionFailed",
					Message: err.Error(),
				})
				r.notifyFailed(ctx, env, err.Error())
				res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, err, 0)
				return ctx, res, true, retErr
			}

			if featResult != nil {
				env.Status.FeatureConfigMap = featResult.ConfigMapName
				env.Status.FeatureEnvVars = featResult.EnvVars
			}

			msg := "Features ready"
			if featResult != nil && featResult.Message != "" {
				msg = featResult.Message
			}
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    "FeaturesReady",
				Status:  metav1.ConditionTrue,
				Reason:  "FeaturesProvisioned",
				Message: msg,
			})
		}
	}

	return ctx, ctrl.Result{}, false, nil
}

func isKeyPrefixAsyncProto(p string) bool {
	switch p {
	case "redis", "valkey", "s3", "gcs", "minio":
		return true
	default:
		return false
	}
}

func isRedisValkeyPair(a, b string) bool {
	return (a == "redis" || a == "valkey") && (b == "redis" || b == "valkey")
}

func (r *EnvironmentReconciler) resolveAsyncProvisioner(route divergeiov1alpha1.AsyncRouteSpec) async.Provisioner {
	if r.AsyncProvisioner == nil {
		return nil
	}
	proto := strings.ToLower(strings.TrimSpace(route.Protocol))
	name := strings.ToLower(strings.TrimSpace(r.AsyncProvisioner.Name()))
	if name == "webhook" || !async.Providers.Has(name) {
		return r.AsyncProvisioner
	}
	if name == proto || isRedisValkeyPair(name, proto) {
		return r.AsyncProvisioner
	}
	deps := registry.Deps{
		Client: r.Client,
		Scheme: r.Scheme,
	}
	if (name != "noop" && async.Providers.Has(proto)) || isKeyPrefixAsyncProto(proto) {
		if prov, err := async.Providers.Create(proto, deps); err == nil && prov != nil {
			return prov
		}
	}
	if isKeyPrefixAsyncProto(name) && !isKeyPrefixAsyncProto(proto) {
		if prov, err := async.Providers.Create("noop", deps); err == nil && prov != nil {
			return prov
		}
	}
	return r.AsyncProvisioner
}

func (r *EnvironmentReconciler) getFeatureProvider(ctx context.Context, env *divergeiov1alpha1.Environment) (features.FeatureProvider, error) {
	providerName := "configmap"
	if env.Spec.Features != nil && env.Spec.Features.Provider != "" {
		providerName = env.Spec.Features.Provider
	}
	if multicluster.IsRemoteEnv(env) {
		targetClient, _, _, err := r.resolveTargetCluster(ctx, env)
		if err != nil {
			return nil, err
		}
		deps := registry.Deps{
			Client: targetClient,
			Scheme: r.Scheme,
		}
		if features.Providers.Has(providerName) {
			return features.Providers.Create(providerName, deps)
		}
		if r.FeatureProvider != nil && r.FeatureProvider.Type() != "configmap" {
			return r.FeatureProvider, nil
		}
		return nil, fmt.Errorf("feature provider %q not registered", providerName)
	}
	if r.FeatureProvider != nil && r.FeatureProvider.Type() == providerName {
		return r.FeatureProvider, nil
	}
	deps := registry.Deps{
		Client: r.Client,
		Scheme: r.Scheme,
	}
	if features.Providers.Has(providerName) {
		return features.Providers.Create(providerName, deps)
	}
	if r.FeatureProvider != nil {
		return r.FeatureProvider, nil
	}
	return nil, fmt.Errorf("feature provider %q not registered", providerName)
}

var allowedEnvDatabaseProviders = map[string]bool{
	"cnpg":     true,
	"fresh":    true,
	"snapshot": true,
	"neon":     true,
	"schema":   true,
	"noop":     true,
	"none":     true,
}

func isClusterScopedDBProvider(name string) bool {
	return name == "cnpg" || name == "fresh" || name == "snapshot"
}

func (r *EnvironmentReconciler) resolveDatabaseProvider(env *divergeiov1alpha1.Environment, forTeardown bool) (pkgdb.DatabaseProvider, string, error) {
	return r.resolveDatabaseProviderWithContext(context.Background(), env, forTeardown)
}

func (r *EnvironmentReconciler) resolveDatabaseProviderWithContext(ctx context.Context, env *divergeiov1alpha1.Environment, forTeardown bool) (pkgdb.DatabaseProvider, string, error) {
	if env != nil {
		clientForProvider := func(providerName string) (client.Client, bool, error) {
			if !isClusterScopedDBProvider(providerName) || !multicluster.IsRemoteEnv(env) {
				return r.Client, false, nil
			}
			target, err := r.resolveTarget(ctx, env)
			if err != nil {
				if forTeardown {
					if _, isVal := multicluster.IsValidationError(err); isVal && !multicluster.HasConfirmedRemoteTarget(env) {
						return nil, true, nil
					}
				}
				return nil, false, err
			}
			return target.Client, false, nil
		}

		recorded := strings.TrimSpace(env.Status.DatabaseProvider)
		if forTeardown && recorded != "" && pkgdb.Providers.Has(recorded) {
			provClient, skip, cErr := clientForProvider(recorded)
			if cErr != nil {
				return nil, recorded, fmt.Errorf("failed to resolve target cluster for database provider %q: %w", recorded, cErr)
			}
			if skip {
				return nil, recorded, nil
			}
			deps := registry.Deps{
				Client: provClient,
				Scheme: r.Scheme,
			}
			prov, err := pkgdb.Providers.Create(recorded, deps)
			if err != nil {
				return nil, recorded, fmt.Errorf("failed to create recorded database provider %q: %w", recorded, err)
			}
			return prov, recorded, nil
		}

		explicit := strings.TrimSpace(env.Annotations["divergedev.com/database-provider"])
		mode := strings.TrimSpace(env.Spec.Database.Mode)
		var candidate string
		if explicit != "" {
			if !allowedEnvDatabaseProviders[explicit] {
				return nil, "", fmt.Errorf("unsupported database provider annotation %q", explicit)
			}
			candidate = explicit
		} else if (mode == "snapshot" || mode == "cnpg" || mode == "fresh" || mode == "neon") && isDefaultOrNoopDBProvider(r.DatabaseProvider) {
			candidate = mode
		}

		if recorded != "" {
			if candidate != "" && candidate != recorded {
				return nil, recorded, fmt.Errorf("cannot change database provider from %q to %q after provisioning", recorded, candidate)
			}
			candidate = recorded
		}

		if candidate != "" {
			if !pkgdb.Providers.Has(candidate) {
				return nil, candidate, fmt.Errorf("database provider %q is not registered", candidate)
			}
			provClient, skip, cErr := clientForProvider(candidate)
			if cErr != nil {
				return nil, candidate, fmt.Errorf("failed to resolve target cluster for database provider %q: %w", candidate, cErr)
			}
			if skip {
				return nil, candidate, nil
			}
			deps := registry.Deps{
				Client: provClient,
				Scheme: r.Scheme,
			}
			prov, err := pkgdb.Providers.Create(candidate, deps)
			if err != nil {
				return nil, candidate, fmt.Errorf("failed to create database provider %q: %w", candidate, err)
			}
			return prov, candidate, nil
		}
	}
	return r.DatabaseProvider, databaseProviderName(r.DatabaseProvider), nil
}

func databaseProviderName(p pkgdb.DatabaseProvider) string {
	if p == nil {
		return ""
	}
	if named, ok := p.(interface{ ProviderName() string }); ok {
		return strings.TrimSpace(named.ProviderName())
	}
	if inst, ok := p.(*internaldb.InstrumentedDatabaseProvider); ok {
		if inner := databaseProviderName(inst.Inner); inner != "" {
			return inner
		}
		if strings.EqualFold(strings.TrimSpace(inst.Mode), "neon") {
			return "neon"
		}
	}
	return ""
}

func isNeonDBProvider(resolvedDBName string, configured pkgdb.DatabaseProvider, env *divergeiov1alpha1.Environment) bool {
	if resolvedDBName == "neon" || databaseProviderName(configured) == "neon" {
		return true
	}
	if env == nil {
		return false
	}
	return env.Spec.Database.Mode == "neon" ||
		strings.TrimSpace(env.Status.DatabaseProvider) == "neon" ||
		strings.TrimSpace(env.Annotations["divergedev.com/database-provider"]) == "neon"
}

func isDefaultOrNoopDBProvider(p pkgdb.DatabaseProvider) bool {
	if p == nil {
		return true
	}
	if _, ok := p.(*internaldb.NoopDatabaseProvider); ok {
		return true
	}
	if inst, ok := p.(*internaldb.InstrumentedDatabaseProvider); ok {
		return inst.Inner == nil || inst.Mode == "" || inst.Mode == "none" || inst.Mode == "noop" || isDefaultOrNoopDBProvider(inst.Inner)
	}
	return false
}

func closeDatabaseProvider(p pkgdb.DatabaseProvider) {
	if p == nil {
		return
	}
	if closer, ok := p.(io.Closer); ok {
		_ = closer.Close()
		return
	}
	if inst, ok := p.(*internaldb.InstrumentedDatabaseProvider); ok && inst.Inner != nil {
		closeDatabaseProvider(inst.Inner)
	}
}

func (r *EnvironmentReconciler) ensureBannerConfigMap(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	bannerSpec := env.Spec.Routing.Banner

	targetClient, _, isRemote, err := r.resolveTargetCluster(ctx, env)
	if err != nil {
		return err
	}

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	cmName := fmt.Sprintf("diverge-banner-%s", env.Name)
	if len(cmName) > 253 {
		cmName = cmName[:253]
	}

	if bannerSpec == nil || !bannerSpec.Enabled {
		existing := &corev1.ConfigMap{}
		if err := targetClient.Get(ctx, types.NamespacedName{Name: cmName, Namespace: targetNS}, existing); err == nil {
			if err := targetClient.Delete(ctx, existing); err != nil {
				return fmt.Errorf("failed to delete banner configmap: %w", err)
			}
		}
		return nil
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: targetNS,
			Labels: map[string]string{
				"divergedev.com/environment": env.Name,
				"divergedev.com/managed-by":  "diverge",
			},
		},
	}

	if !isRemote && targetNS == env.Namespace {
		if err := controllerutil.SetControllerReference(env, cm, r.Scheme); err != nil {
			return fmt.Errorf("failed to set owner reference: %w", err)
		}
	}

	script := banner.Script
	text := "Preview Environment"
	if bannerSpec.Text != "" {
		text = bannerSpec.Text
	}
	color := "#FF6B00"
	if bannerSpec.Color != "" {
		color = bannerSpec.Color
	}
	position := "top"
	if bannerSpec.Position != "" {
		position = bannerSpec.Position
	}

	configJSON, err := json.Marshal(map[string]string{
		"text":     text,
		"branch":   env.Spec.Source.Branch,
		"color":    color,
		"position": position,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal banner config: %w", err)
	}
	script = strings.ReplaceAll(script, "{{CONFIG_JSON}}", string(configJSON))

	_, err = controllerutil.CreateOrUpdate(ctx, targetClient, cm, func() error {
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data["diverge-banner.js"] = script
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or update banner ConfigMap: %w", err)
	}
	return nil
}

func (r *EnvironmentReconciler) ensureDatabaseEnvSecret(ctx context.Context, env *divergeiov1alpha1.Environment, envVars map[string]string) error {
	targetClient, _, isRemote, err := r.resolveTargetCluster(ctx, env)
	if err != nil {
		return err
	}

	secretName := internaldb.EnvSecretName(env.Name)

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: targetNS,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, targetClient, secret, func() error {
		prevGroup := ""
		if secret.Labels != nil {
			prevGroup = strings.TrimSpace(secret.Labels[labelPreviewGroup])
		}
		wasGroupOwned := (secret.Labels != nil && secret.Labels[labelSharedGroupDBSecret] == "true") ||
			(prevGroup != "" && !hasEnvironmentControllerOwnerRef(secret))

		if secret.Labels == nil {
			secret.Labels = make(map[string]string)
		}
		secret.Labels["divergedev.com/environment"] = truncateLabel(env.Name)
		secret.Labels["divergedev.com/managed-by"] = "diverge"
		delete(secret.Labels, labelPreviewGroup)
		delete(secret.Labels, labelSharedGroupDBSecret)

		if !isRemote && targetNS == env.Namespace {
			if r.Scheme != nil {
				if err := controllerutil.SetControllerReference(env, secret, r.Scheme); err != nil {
					return err
				}
			} else {
				for _, ref := range secret.OwnerReferences {
					if ref.Controller != nil && *ref.Controller && (ref.Kind != "Environment" || ref.Name != env.Name) {
						return fmt.Errorf("secret %s is already owned by %s/%s", secret.Name, ref.Kind, ref.Name)
					}
				}
				if env.UID != "" {
					isCtrl := true
					secret.OwnerReferences = []metav1.OwnerReference{{
						APIVersion:         "divergedev.com/v1alpha1",
						Kind:               "Environment",
						Name:               env.Name,
						UID:                env.UID,
						Controller:         &isCtrl,
						BlockOwnerDeletion: &isCtrl,
					}}
				}
			}
		}

		if wasGroupOwned {
			if prevGroup != "" {
				secret.Labels[labelLegacyGroupDBDrainingOwner] = prevGroup
			}
			if secret.Annotations == nil {
				secret.Annotations = make(map[string]string)
			}
			secret.Annotations[annotationLegacyGroupDBDraining] = legacyGroupDBDrainingPending
			if prevGroup != "" {
				secret.Annotations[annotationLegacyGroupDBOwner] = prevGroup
			}
		}
		if wasGroupOwned || strings.TrimSpace(secret.Annotations[internaldb.AnnotationDBCredentialRevision]) != "" {
			if credRev := internaldb.CredentialRevision(envVars); credRev != "" {
				if secret.Annotations == nil {
					secret.Annotations = make(map[string]string)
				}
				secret.Annotations[internaldb.AnnotationDBCredentialRevision] = credRev
			}
		}

		newData := make(map[string][]byte, len(envVars))
		for k, v := range envVars {
			newData[k] = []byte(v)
		}
		secret.Data = newData
		secret.StringData = nil
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or update database env secret: %w", err)
	}

	if secret.Annotations[annotationLegacyGroupDBDraining] == legacyGroupDBDrainingPending {
		var podList corev1.PodList
		if lErr := targetClient.List(ctx, &podList, client.InNamespace(targetNS)); lErr != nil {
			if !isIgnorableWorkloadListErr(lErr, false) {
				return fmt.Errorf("failed to list pods in %s after legacy DB Secret takeover: %w", targetNS, lErr)
			}
		}
		seenIDs := make(map[string]bool)
		var podIDs []string
		for i := range podList.Items {
			pod := &podList.Items[i]
			if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				continue
			}
			if podSpecReferencesAnySecret(&pod.Spec, []string{secretName}) {
				id := podTrackID(pod)
				if !seenIDs[id] {
					seenIDs[id] = true
					podIDs = append(podIDs, id)
				}
			}
		}
		sort.Strings(podIDs)
		base := secret.DeepCopy()
		if len(podIDs) > 0 {
			if secret.Annotations == nil {
				secret.Annotations = make(map[string]string)
			}
			secret.Annotations[annotationLegacyGroupDBDraining] = legacyGroupDBDrainingUIDPrefix + strings.Join(podIDs, ",")
		} else {
			delete(secret.Annotations, annotationLegacyGroupDBDraining)
			delete(secret.Annotations, annotationLegacyGroupDBOwner)
			delete(secret.Labels, labelLegacyGroupDBDrainingOwner)
		}
		if pErr := targetClient.Patch(ctx, secret, client.MergeFrom(base)); pErr != nil && !apierrors.IsNotFound(pErr) {
			return fmt.Errorf("failed to record draining pods on legacy DB Secret %s/%s: %w", targetNS, secretName, pErr)
		}
	}
	if credRev := strings.TrimSpace(secret.Annotations[internaldb.AnnotationDBCredentialRevision]); credRev != "" {
		if env.Annotations == nil {
			env.Annotations = make(map[string]string)
		}
		env.Annotations[internaldb.AnnotationDBCredentialRevision] = credRev
	}
	return nil
}

func (r *EnvironmentReconciler) readSharedGroupDatabaseDSN(ctx context.Context, env *divergeiov1alpha1.Environment) (string, string, bool, error) {
	targetClient, _, _, err := r.resolveTargetCluster(ctx, env)
	if err != nil {
		return "", "", false, err
	}
	if targetClient == nil {
		return "", "", false, fmt.Errorf("target cluster client unavailable for shared database lookup")
	}
	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}
	var sec corev1.Secret
	secretName := strings.TrimSpace(env.Spec.Database.ConnectionRef)
	if secretName != "" {
		if err := targetClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: targetNS}, &sec); err != nil {
			if apierrors.IsNotFound(err) {
				return "", "", false, nil
			}
			return "", "", false, fmt.Errorf("failed to get shared group database secret %s/%s: %w", targetNS, secretName, err)
		}
	} else {
		secretName = sharedGroupDBSecretName(env.Name)
		if err := targetClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: targetNS}, &sec); err != nil {
			if !apierrors.IsNotFound(err) {
				return "", "", false, fmt.Errorf("failed to get shared group database secret %s/%s: %w", targetNS, secretName, err)
			}
			secretName = legacySharedGroupDBSecretName(env.Name)
			if lErr := targetClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: targetNS}, &sec); lErr != nil {
				if apierrors.IsNotFound(lErr) {
					return "", "", false, nil
				}
				return "", "", false, fmt.Errorf("failed to get shared group database secret %s/%s: %w", targetNS, secretName, lErr)
			}
		}
	}
	dsn := strings.TrimSpace(string(sec.Data["DATABASE_URL"]))
	if dsn == "" && len(sec.Data) == 0 {
		return "", "", false, nil
	}
	if dsn == "" && (env.Spec.Database.MigrationJob != nil || env.Spec.Database.Atlas != nil) {
		return "", "", false, fmt.Errorf("shared group database secret %s/%s is missing DATABASE_URL", targetNS, secretName)
	}
	if dsn == "" && env.Spec.ServiceConfig != nil && strings.TrimSpace(env.Spec.ServiceConfig.DatabaseEnvKey) != "" {
		return "", "", false, nil
	}
	return secretName, dsn, true, nil
}
