package controller

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/events"
	"github.com/divergedev/diverge/internal/metrics"
	"github.com/divergedev/diverge/internal/notifier"
	"github.com/divergedev/diverge/pkg/database"
)

const (
	previewGroupFinalizer = "diverge.io/previewgroup-protection"
	labelPreviewGroup     = "diverge.io/previewgroup"
	labelManagedBy        = "diverge.io/managed-by"
)

// PreviewGroupReconciler reconciles a PreviewGroup object.
// It acts as an "operator of operators", creating and managing child
// Environment CRs for each service in the group.
type PreviewGroupReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Recorder         *events.Recorder
	Notifier         notifier.PreviewGroupNotifier
	StatusReporter   notifier.StatusReporter
	DatabaseProvider database.DatabaseProvider
	EnableGAMMA      bool // Enable GAMMA mesh routing (requires Istio Ambient)
}

// +kubebuilder:rbac:groups=diverge.io,resources=previewgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=diverge.io,resources=previewgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=diverge.io,resources=previewgroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=diverge.io,resources=environments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes;grpcroutes,verbs=list;delete
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=list;delete

// Reconcile performs its designated operation.
func (r *PreviewGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	logger := log.FromContext(ctx)

	var pg divergeiov1alpha1.PreviewGroup
	if err := r.Get(ctx, req.NamespacedName, &pg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	startTime := time.Now()
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("previewgroup").Observe(time.Since(startTime).Seconds())
		if retErr != nil {
			metrics.ReconcileTotal.WithLabelValues("previewgroup", "error").Inc()
		} else {
			metrics.ReconcileTotal.WithLabelValues("previewgroup", "success").Inc()
		}
	}()

	// Capture pre-mutation baseline for status patch
	statusBase := pg.DeepCopy()

	// Handle deletion
	if !pg.DeletionTimestamp.IsZero() {
		return r.handleTeardown(ctx, &pg)
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(&pg, previewGroupFinalizer) {
		controllerutil.AddFinalizer(&pg, previewGroupFinalizer)
		if err := r.Update(ctx, &pg); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		metrics.PreviewGroupsActive.Inc()
		r.Recorder.Event(&pg, "Normal", "Created", "PreviewGroup created")
		return ctrl.Result{}, nil
	}

	// Set ObservedGeneration
	pg.Status.ObservedGeneration = pg.Generation

	// Set CreatedAt on first reconcile
	if pg.Status.CreatedAt == nil {
		now := metav1.Now()
		pg.Status.CreatedAt = &now
	}

	// Set TTL expiry
	var requeueAfter time.Duration
	if pg.Spec.Lifecycle != nil && pg.Spec.Lifecycle.TTL != nil && pg.Status.CreatedAt != nil {
		expiryTime := pg.Status.CreatedAt.Add(pg.Spec.Lifecycle.TTL.Duration)
		pg.Status.ExpiresAt = &metav1.Time{Time: expiryTime}
		if time.Now().After(expiryTime) {
			logger.Info("PreviewGroup TTL expired, triggering deletion")
			if err := r.cleanupRoutesAndEndpoints(ctx, pg.Name); err != nil {
				return ctrl.Result{}, err
			}
			if err := r.Delete(ctx, &pg); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete expired PreviewGroup: %w", err)
			}
			return ctrl.Result{}, nil
		}
		requeueAfter = time.Until(expiryTime)
	}

	if pg.Status.LeaseRenewedAt != nil {
		expiration := pg.Status.LeaseRenewedAt.Add(90 * time.Second)
		if time.Now().After(expiration) {
			logger.Info("PreviewGroup lease expired, marking abandoned and triggering deletion")
			pg.Status.Phase = divergeiov1alpha1.PreviewGroupPhaseAbandoned
			if err := r.Status().Patch(ctx, &pg, client.MergeFrom(statusBase)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update PreviewGroup status: %w", err)
			}

			if err := r.cleanupRoutesAndEndpoints(ctx, pg.Name); err != nil {
				return ctrl.Result{}, err
			}

			if err := r.Delete(ctx, &pg); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete abandoned PreviewGroup: %w", err)
			}
			return ctrl.Result{}, nil
		}

		d := time.Until(expiration)
		if requeueAfter == 0 || d < requeueAfter {
			requeueAfter = d
		}
	}

	// Reconcile child Environments for each service
	desiredEnvNames := make(map[string]bool)
	serviceStatuses := make([]divergeiov1alpha1.PreviewGroupServiceStatus, 0, len(pg.Spec.Services))
	var requeue bool

	if errs := validation.IsValidLabelValue(pg.Name); len(errs) > 0 {
		pg.Status.Phase = divergeiov1alpha1.PreviewGroupPhaseFailed
		readyCondition := metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "InvalidName",
			Message:            fmt.Sprintf("PreviewGroup name %q is invalid as a label value: %s", pg.Name, strings.Join(errs, ", ")),
			ObservedGeneration: pg.Generation,
		}
		meta.SetStatusCondition(&pg.Status.Conditions, readyCondition)
		if err := r.Status().Patch(ctx, &pg, client.MergeFrom(statusBase)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update PreviewGroup status: %w", err)
		}
		return ctrl.Result{}, nil
	}

	for _, svc := range pg.Spec.Services {
		envName := childEnvironmentName(pg.Name, svc.Name)
		desiredEnvNames[envName] = true

		// Determine target namespace — use service's namespace or default
		targetNS := svc.Namespace
		if targetNS == "" {
			targetNS = "default"
		}

		svcStatus := divergeiov1alpha1.PreviewGroupServiceStatus{
			Name:            svc.Name,
			EnvironmentName: envName,
			Namespace:       targetNS,
		}

		// Skip creating Environment for baseline services —
		// they use the existing service as-is, routing only
		if svc.Mode == divergeiov1alpha1.ServiceModeBaseline {
			svcStatus.Phase = divergeiov1alpha1.PhaseRunning
			svcStatus.Message = "Using baseline service"
			serviceStatuses = append(serviceStatuses, svcStatus)
			continue
		}

		// Build the desired child Environment CR
		desiredEnv := r.buildChildEnvironment(&pg, svc, envName, targetNS)

		// Create or update
		var existingEnv divergeiov1alpha1.Environment
		isNew := false
		err := r.Get(ctx, types.NamespacedName{Name: envName, Namespace: targetNS}, &existingEnv)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to get child Environment %s/%s: %w", targetNS, envName, err)
			}
			// Create
			if err := r.Create(ctx, desiredEnv); err != nil {
				if apierrors.IsAlreadyExists(err) {
					// Race condition — requeue
					requeue = true
					svcStatus.Phase = divergeiov1alpha1.PhasePending
					svcStatus.Message = "Environment already exists, resyncing"
					svcStatus.Reason = "CreateConflict"
					serviceStatuses = append(serviceStatuses, svcStatus)
					continue
				}
				svcStatus.Phase = divergeiov1alpha1.PhaseFailed
				svcStatus.Message = fmt.Sprintf("Failed to create Environment: %v", err)
				svcStatus.Reason = "CreateFailed"
				serviceStatuses = append(serviceStatuses, svcStatus)
				continue
			}
			svcStatus.Phase = divergeiov1alpha1.PhasePending
			svcStatus.Message = "Environment created"
			svcStatus.ChangedServices = desiredEnv.Spec.Deploy.ChangedServices
			existingEnv = *desiredEnv
			isNew = true
		} else {
			// Update — sync spec if changed
			if r.needsUpdate(&existingEnv, desiredEnv) {
				existingEnv.Spec = desiredEnv.Spec
				existingEnv.Labels = desiredEnv.Labels
				if err := r.Update(ctx, &existingEnv); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to update child Environment %s/%s: %w", targetNS, envName, err)
				}
				r.Recorder.Eventf(&pg, "Normal", "ChildUpdated",
					"Updated Environment %s/%s for service %s", targetNS, envName, svc.Name)
			}
			// Copy status from child
			svcStatus.Phase = existingEnv.Status.Phase
			svcStatus.URL = existingEnv.Status.URL
			svcStatus.ChangedServices = existingEnv.Spec.Deploy.ChangedServices
			if existingEnv.Status.Phase == divergeiov1alpha1.PhaseFailed {
				for _, cond := range existingEnv.Status.Conditions {
					if cond.Status == metav1.ConditionFalse {
						svcStatus.Message = cond.Message
						svcStatus.Reason = cond.Reason
						break
					}
				}
			}
		}

		// Provision database if configured
		var dbRes *database.DatabaseResult
		if existingEnv.Spec.Database.Mode != "" && r.DatabaseProvider != nil {
			dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			res, err := r.DatabaseProvider.Provision(dbCtx, &existingEnv)
			cancel()
			if err != nil {
				logger.Error(err, "failed to provision database for child Environment")
			} else {
				dbRes = res
				if isNew && res != nil && len(res.EnvVars) > 0 {
					for k, v := range res.EnvVars {
						existingEnv.Spec.ServiceConfig.Env = append(existingEnv.Spec.ServiceConfig.Env, divergeiov1alpha1.EnvVar{
							Name:  k,
							Value: v,
						})
					}
					if err := r.Update(ctx, &existingEnv); err != nil {
						logger.Error(err, "failed to update child Environment with database env vars")
					}
				}
			}
		}

		// Run database migrations
		if dbRes != nil {
			if err := r.runMigrations(ctx, &existingEnv, dbRes); err != nil {
				if errors.Is(err, ErrHookInProgress) {
					requeue = true
					svcStatus.Phase = divergeiov1alpha1.PhaseDeploying
					svcStatus.Reason = "MigrationRunning"
					svcStatus.Message = "Migration is currently running"
				} else {
					logger.Error(err, "migration failed for child Environment")
					svcStatus.Phase = divergeiov1alpha1.PhaseFailed
					svcStatus.Reason = "MigrationFailed"
					svcStatus.Message = fmt.Sprintf("Migration failed: %v", err)
				}
				serviceStatuses = append(serviceStatuses, svcStatus)
				continue
			}
		}

		// Run post-deploy hooks
		if svc.PostDeploy != nil && svcStatus.Phase == divergeiov1alpha1.PhaseRunning {
			if err := r.runPostDeployJob(ctx, &existingEnv, svc.PostDeploy); err != nil {
				if errors.Is(err, ErrHookInProgress) {
					blocking := svc.PostDeploy.Blocking != nil && *svc.PostDeploy.Blocking
					if blocking {
						requeue = true
						svcStatus.Phase = divergeiov1alpha1.PhaseDeploying
						svcStatus.Reason = "PostDeployRunning"
						svcStatus.Message = "Post-deploy hook is currently running"
					}
				} else {
					logger.Error(err, "post-deploy hook failed for child Environment")
					blocking := svc.PostDeploy.Blocking != nil && *svc.PostDeploy.Blocking
					if blocking {
						svcStatus.Phase = divergeiov1alpha1.PhaseFailed
						svcStatus.Reason = "PostDeployFailed"
						svcStatus.Message = fmt.Sprintf("Post-deploy hook failed: %v", err)
					}
				}
			}
		}

		serviceStatuses = append(serviceStatuses, svcStatus)
	}

	// Delete orphaned Environments (services removed from spec)
	if err := r.deleteOrphanedEnvironments(ctx, &pg, desiredEnvNames); err != nil {
		logger.Error(err, "failed to delete orphaned Environments")
	}

	// Update status
	pg.Status.Services = serviceStatuses
	pg.Status.ServiceCount = int32(len(pg.Spec.Services))
	pg.Status.Phase = derivePreviewGroupPhase(serviceStatuses)

	// Set conditions
	readyCondition := metav1.Condition{
		Type:               "Ready",
		ObservedGeneration: pg.Generation,
	}
	switch pg.Status.Phase {
	case divergeiov1alpha1.PreviewGroupPhaseRunning:
		readyCondition.Status = metav1.ConditionTrue
		readyCondition.Reason = "AllServicesRunning"
		readyCondition.Message = fmt.Sprintf("All %d services are running", pg.Status.ServiceCount)
	case divergeiov1alpha1.PreviewGroupPhaseDegraded:
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = "ServicesDegraded"
		readyCondition.Message = "Some services are not running"
	case divergeiov1alpha1.PreviewGroupPhaseFailed:
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = "ServicesFailed"
		readyCondition.Message = "All services have failed"
	default:
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = "ServicesProvisioning"
		readyCondition.Message = "Services are being provisioned"
	}
	meta.SetStatusCondition(&pg.Status.Conditions, readyCondition)

	// Persist status
	patch := client.MergeFrom(statusBase)
	if err := r.Status().Patch(ctx, &pg, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update PreviewGroup status: %w", err)
	}

	if r.Notifier != nil {
		notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := r.Notifier.UpdateGroupStatus(notifyCtx, &pg); err != nil {
			logger.Error(err, "failed to update group status notification")
		}
		cancel()

		prevPhase := statusBase.Status.Phase
		if pg.Status.Phase != prevPhase {
			switch pg.Status.Phase {
			case divergeiov1alpha1.PreviewGroupPhaseRunning:
				notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				if err := r.Notifier.PostGroupReady(notifyCtx, &pg); err != nil {
					logger.Error(err, "failed to post group ready notification")
				}
				cancel()
			case divergeiov1alpha1.PreviewGroupPhaseFailed, divergeiov1alpha1.PreviewGroupPhaseDegraded:
				notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				if err := r.Notifier.PostGroupFailed(notifyCtx, &pg, readyCondition.Reason); err != nil {
					logger.Error(err, "failed to post group failed notification")
				}
				cancel()
			}
		}
	}

	if requeueAfter > 0 {
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	if requeue {
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

// handleTeardown deletes all child Environments and removes the finalizer.
func (r *PreviewGroupReconciler) handleTeardown(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(pg, previewGroupFinalizer) {
		return ctrl.Result{}, nil
	}

	r.Recorder.Event(pg, "Normal", "Terminating", "Teardown started")

	// List all child Environments
	children, err := r.listChildEnvironments(ctx, pg)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list child Environments: %w", err)
	}

	// Delete children that haven't been deleted yet
	remaining := 0
	for i := range children {
		child := &children[i]
		if child.DeletionTimestamp.IsZero() {
			if r.DatabaseProvider != nil && child.Spec.Database.Mode != "" {
				dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				if err := r.DatabaseProvider.Teardown(dbCtx, child); err != nil {
					logger.Error(err, "failed to teardown database for child Environment", "name", child.Name)
				}
				cancel()
			}

			if err := r.Delete(ctx, child); err != nil && !apierrors.IsNotFound(err) {
				logger.Error(err, "failed to delete child Environment", "name", child.Name, "namespace", child.Namespace)
				remaining++
				continue
			}
			r.Recorder.Eventf(pg, "Normal", "ChildDeleted",
				"Deleted Environment %s/%s", child.Namespace, child.Name)
		}
		remaining++
	}

	// Requeue if children are still terminating
	if remaining > 0 {
		logger.Info("Waiting for child Environments to terminate", "remaining", remaining)
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}

	// All children gone — remove finalizer
	if r.Notifier != nil {
		notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := r.Notifier.PostGroupTeardown(notifyCtx, pg); err != nil {
			logger.Error(err, "failed to post group teardown notification")
		}
		cancel()
	}

	controllerutil.RemoveFinalizer(pg, previewGroupFinalizer)
	if err := r.Update(ctx, pg); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}
	metrics.PreviewGroupsActive.Dec()

	r.Recorder.Event(pg, "Normal", "Terminated", "Teardown complete")
	return ctrl.Result{}, nil
}

// buildChildEnvironment constructs a child Environment CR from a PreviewGroup service spec.
func (r *PreviewGroupReconciler) buildChildEnvironment(
	pg *divergeiov1alpha1.PreviewGroup,
	svc divergeiov1alpha1.PreviewGroupServiceSpec,
	envName, targetNS string,
) *divergeiov1alpha1.Environment {
	// Determine database config — service-level overrides group-level
	dbConfig := divergeiov1alpha1.EnvironmentDatabase{}
	if pg.Spec.Database != nil {
		dbConfig = *pg.Spec.Database
	}
	if svc.Database != nil {
		dbConfig = *svc.Database
	}

	// Build the routing config from group
	routingConfig := divergeiov1alpha1.EnvironmentRouting{
		Mode:        pg.Spec.Routing.Mode,
		Provider:    "gateway",
		HeaderKey:   pg.Spec.Routing.HeaderKey,
		HeaderValue: pg.Spec.Routing.HeaderValue,
		ExternalURL: pg.Spec.Routing.ExternalURL,
		AsyncRoutes: svc.AsyncRoutes,
	}
	if routingConfig.Mode == "" {
		routingConfig.Mode = "header"
	}
	if routingConfig.HeaderKey == "" {
		routingConfig.HeaderKey = "x-preview-env"
	}

	var svcName string
	if r.EnableGAMMA {
		svcName = svc.Name
	}

	// Build service config
	serviceConfig := &divergeiov1alpha1.ServicePreviewConfig{
		ServiceName:     svcName,
		Namespace:       targetNS,
		Port:            svc.Port,
		Image:           svc.Image,
		ImagePullPolicy: svc.ImagePullPolicy,
		ParentRef:       svc.ParentRef,
		PathPrefix:      svc.PathPrefix,
		HeaderKey:       pg.Spec.Routing.HeaderKey,
		Env:             svc.Env,
		Protocol:        string(svc.Protocol),
		Resources:       svc.Resources,
		KEDA:            svc.KEDA,
	}

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      envName,
			Namespace: targetNS,
			Labels: map[string]string{
				labelPreviewGroup: pg.Name,
				labelManagedBy:    "diverge-previewgroup",
			},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source:        pg.Spec.Source,
			Routing:       routingConfig,
			Database:      dbConfig,
			ServiceConfig: serviceConfig,
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Mode:            "delta",
				Namespace:       "same",
				ChangedServices: []string{svc.Name},
			},
		},
	}

	// Lifecycle — copy TTL/cleanup from group
	if pg.Spec.Lifecycle != nil {
		env.Spec.Lifecycle = divergeiov1alpha1.EnvironmentLifecycle{
			TTL:            pg.Spec.Lifecycle.TTL,
			CleanupOnMerge: pg.Spec.Lifecycle.CleanupOnMerge,
		}
	}

	return env
}

// needsUpdate checks if the existing Environment's spec has drifted from desired.
func (r *PreviewGroupReconciler) needsUpdate(existing, desired *divergeiov1alpha1.Environment) bool {
	// Compare full spec — defaultProtocol() in buildChildEnvironment prevents
	// false drift from omitted protocol fields.
	if !equality.Semantic.DeepEqual(existing.Spec, desired.Spec) {
		return true
	}
	// Compare labels to catch metadata drift
	for k, v := range desired.Labels {
		if existing.Labels[k] != v {
			return true
		}
	}
	return false
}

// listChildEnvironments returns all Environments owned by this PreviewGroup.
func (r *PreviewGroupReconciler) listChildEnvironments(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) ([]divergeiov1alpha1.Environment, error) {
	var envList divergeiov1alpha1.EnvironmentList
	if err := r.List(ctx, &envList,
		client.InNamespace(pg.Namespace),
		client.MatchingLabels{
			"diverge.io/previewgroup": pg.Name,
			labelManagedBy:            "diverge-previewgroup",
		},
	); err != nil {
		return nil, err
	}
	return envList.Items, nil
}

// deleteOrphanedEnvironments removes child Environments that are no longer in the spec.
func (r *PreviewGroupReconciler) deleteOrphanedEnvironments(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup, desired map[string]bool) error {
	logger := log.FromContext(ctx)

	children, err := r.listChildEnvironments(ctx, pg)
	if err != nil {
		return err
	}

	for i := range children {
		child := &children[i]
		if !desired[child.Name] {
			logger.Info("Deleting orphaned Environment", "name", child.Name, "namespace", child.Namespace)
			if err := r.Delete(ctx, child); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete orphaned Environment %s/%s: %w", child.Namespace, child.Name, err)
			}
			r.Recorder.Eventf(pg, "Normal", "OrphanDeleted",
				"Deleted orphaned Environment %s/%s", child.Namespace, child.Name)
		}
	}
	return nil
}

// derivePreviewGroupPhase computes the aggregate phase from per-service statuses.
func derivePreviewGroupPhase(services []divergeiov1alpha1.PreviewGroupServiceStatus) divergeiov1alpha1.PreviewGroupPhase {
	if len(services) == 0 {
		return divergeiov1alpha1.PreviewGroupPhasePending
	}

	var running, failed, deploying, pending int
	for _, svc := range services {
		switch svc.Phase {
		case divergeiov1alpha1.PhaseRunning:
			running++
		case divergeiov1alpha1.PhaseFailed:
			failed++
		case divergeiov1alpha1.PhaseDeploying:
			deploying++
		default:
			pending++
		}
	}

	total := len(services)
	switch {
	case running == total:
		return divergeiov1alpha1.PreviewGroupPhaseRunning
	case failed == total:
		return divergeiov1alpha1.PreviewGroupPhaseFailed
	case failed > 0 && running > 0:
		return divergeiov1alpha1.PreviewGroupPhaseDegraded
	case deploying > 0:
		return divergeiov1alpha1.PreviewGroupPhaseDeploying
	default:
		return divergeiov1alpha1.PreviewGroupPhasePending
	}
}

// childEnvironmentName generates a DNS-safe name for a child Environment.
// Format: pg-{group}-{service}-{hash8}
func childEnvironmentName(groupName, serviceName string) string {
	raw := fmt.Sprintf("pg-%s-%s", groupName, serviceName)
	raw = strings.ToLower(raw)
	raw = strings.NewReplacer(".", "-", "_", "-").Replace(raw)
	raw = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(raw, "")
	raw = regexp.MustCompile(`-{2,}`).ReplaceAllString(raw, "-")
	raw = strings.Trim(raw, "-")

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(groupName+"/"+serviceName)))[:8]

	if len(raw) <= 63-9 {
		return raw + "-" + hash
	}
	return raw[:63-9] + "-" + hash
}

// cleanupRoutesAndEndpoints removes routing resources for a PreviewGroup
func (r *PreviewGroupReconciler) cleanupRoutesAndEndpoints(ctx context.Context, pgName string) error {
	// Delete HTTPRoute
	err := func() error {
		cleanupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var httpRouteList unstructured.UnstructuredList
		httpRouteList.SetAPIVersion("gateway.networking.k8s.io/v1")
		httpRouteList.SetKind("HTTPRouteList")
		if err := r.List(cleanupCtx, &httpRouteList, client.MatchingLabels{"diverge.io/previewgroup": pgName}); err != nil {
			return fmt.Errorf("failed to list HTTPRoutes: %w", err)
		}
		for i := range httpRouteList.Items {
			if err := r.Delete(cleanupCtx, &httpRouteList.Items[i]); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete HTTPRoute: %w", err)
			}
		}
		return nil
	}()
	if err != nil {
		return err
	}

	// Delete GRPCRoute
	err = func() error {
		cleanupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var grpcRouteList unstructured.UnstructuredList
		grpcRouteList.SetAPIVersion("gateway.networking.k8s.io/v1alpha2")
		grpcRouteList.SetKind("GRPCRouteList")
		if err := r.List(cleanupCtx, &grpcRouteList, client.MatchingLabels{"diverge.io/previewgroup": pgName}); err != nil {
			return fmt.Errorf("failed to list GRPCRoutes: %w", err)
		}
		for i := range grpcRouteList.Items {
			if err := r.Delete(cleanupCtx, &grpcRouteList.Items[i]); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete GRPCRoute: %w", err)
			}
		}
		return nil
	}()
	if err != nil {
		return err
	}

	// Delete EndpointSlices
	err = func() error {
		cleanupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var endpointSliceList unstructured.UnstructuredList
		endpointSliceList.SetAPIVersion("discovery.k8s.io/v1")
		endpointSliceList.SetKind("EndpointSliceList")
		if err := r.List(cleanupCtx, &endpointSliceList, client.MatchingLabels{
			"diverge.io/previewgroup":                pgName,
			"endpointslice.kubernetes.io/managed-by": "diverge",
		}); err != nil {
			return fmt.Errorf("failed to list EndpointSlices: %w", err)
		}
		for i := range endpointSliceList.Items {
			eps := &endpointSliceList.Items[i]
			uid := eps.GetUID()
			if err := r.Delete(cleanupCtx, eps, &client.DeleteOptions{
				Preconditions: &metav1.Preconditions{UID: &uid},
			}); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete EndpointSlice: %w", err)
			}
		}
		return nil
	}()
	if err != nil {
		return err
	}
	return nil
}

// mapEnvironmentToGroup maps a child Environment event back to its parent PreviewGroup.
func (r *PreviewGroupReconciler) mapEnvironmentToGroup(_ context.Context, obj client.Object) []reconcile.Request {
	env, ok := obj.(*divergeiov1alpha1.Environment)
	if !ok {
		return nil
	}
	groupName, exists := env.Labels[labelPreviewGroup]
	if !exists {
		return nil
	}
	// PreviewGroup is cluster-scoped — no namespace
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: groupName}},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *PreviewGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&divergeiov1alpha1.PreviewGroup{}).
		Watches(&divergeiov1alpha1.Environment{},
			handler.EnqueueRequestsFromMapFunc(r.mapEnvironmentToGroup),
		).
		Complete(r)
}
