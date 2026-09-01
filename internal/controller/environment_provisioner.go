package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"golang.org/x/sync/errgroup"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/config/banner"
	"github.com/divergedev/diverge/internal/async"
)

func (r *EnvironmentReconciler) notifyFailed(ctx context.Context, env *divergeiov1alpha1.Environment, msg string) {
	if r.Notifier == nil {
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
	ctx, span := otel.Tracer("diverge").Start(ctx, "reconcileProvisioning")
	defer span.End()

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
			return res, true, retErr
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
			return res, true, retErr
		}
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
		return res, true, retErr
	}
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:    "NamespaceReady",
		Status:  metav1.ConditionTrue,
		Reason:  "NamespaceProvisioned",
		Message: "Namespace is ready",
	})

	// 6. Ensure database
	tCtxDB, cancelDB := context.WithTimeout(ctx, 15*time.Second)
	defer cancelDB()
	dbStatus, err := r.DatabaseProvider.Provision(tCtxDB, env)
	if err != nil {
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "DatabaseReady",
			Status:  metav1.ConditionFalse,
			Reason:  "DatabaseProvisionFailed",
			Message: err.Error(),
		})
		r.notifyFailed(ctx, env, err.Error())
		res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, err, 0)
		return res, true, retErr
	}
	if dbStatus != nil && dbStatus.Ready {
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "DatabaseReady",
			Status:  metav1.ConditionTrue,
			Reason:  "DatabaseProvisioned",
			Message: "Database is ready",
		})
		env.Status.DatabaseStatus = dbStatus.Message

		// Run migrations (Atlas / MigrationJob) if configured
		if env.Spec.Database.Atlas != nil || env.Spec.Database.MigrationJob != nil {
			migCtx, migCancel := context.WithTimeout(ctx, 60*time.Second)
			defer migCancel()
			if migErr := r.runMigrations(migCtx, env, dbStatus); migErr != nil {
				logger.Error(migErr, "database migration failed")
				r.Recorder.Event(env, "Warning", "MigrationFailed", migErr.Error())
			}
		}
	} else {
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    "DatabaseReady",
			Status:  metav1.ConditionFalse,
			Reason:  "DatabaseProvisioning",
			Message: "Database is provisioning",
		})
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
		return res, true, retErr
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
					return res, true, retErr
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
					result, err := r.AsyncProvisioner.Provision(tCtxA, envCopy, route)
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
				return res, true, retErr
			}

			// Merge env vars sequentially to detect collisions deterministically
			for _, rr := range results {
				for k, v := range rr.envVars {
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
						return res, true, retErr
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

	return ctrl.Result{}, false, nil
}

func (r *EnvironmentReconciler) ensureBannerConfigMap(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	bannerSpec := env.Spec.Routing.Banner

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
		if err := r.Get(ctx, types.NamespacedName{Name: cmName, Namespace: targetNS}, existing); err == nil {
			if err := r.Delete(ctx, existing); err != nil {
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
				"diverge.io/environment": env.Name,
				"diverge.io/managed-by":  "diverge",
			},
		},
	}

	if targetNS == env.Namespace {
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

	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
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
