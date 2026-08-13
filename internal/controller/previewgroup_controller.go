package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/database"
	"github.com/divergedev/diverge/internal/notifier"
)

const (
	previewGroupFinalizer = "diverge.io/previewgroup-protection"
	labelPreviewGroup     = "diverge.io/preview-group"
	labelManagedBy        = "diverge.io/managed-by"
)

// PreviewGroupReconciler reconciles a PreviewGroup object.
// It acts as an "operator of operators", creating and managing child
// Environment CRs for each service in the group.
type PreviewGroupReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Recorder         record.EventRecorder
	Notifier         notifier.PreviewGroupNotifier
	StatusReporter   notifier.StatusReporter
	DatabaseProvider database.DatabaseProvider
	EnableGAMMA      bool // Enable GAMMA mesh routing (requires Istio Ambient)
}

// +kubebuilder:rbac:groups=diverge.io,resources=previewgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=diverge.io,resources=previewgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=diverge.io,resources=previewgroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=diverge.io,resources=environments,verbs=get;list;watch;create;update;patch;delete

func (r *PreviewGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	logger := log.FromContext(ctx)

	var pg divergeiov1alpha1.PreviewGroup
	if err := r.Get(ctx, req.NamespacedName, &pg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

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
			if err := r.Delete(ctx, &pg); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete expired PreviewGroup: %w", err)
			}
			return ctrl.Result{}, nil
		}
		requeueAfter = time.Until(expiryTime)
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

			// Provision database if configured
			if desiredEnv.Spec.Database.Mode != "" && r.DatabaseProvider != nil {
				res, err := r.DatabaseProvider.Provision(ctx, desiredEnv)
				if err != nil {
					logger.Error(err, "failed to provision database for child Environment")
				} else if res != nil && len(res.EnvVars) > 0 {
					// Inject database env vars into child environment
					for k, v := range res.EnvVars {
						desiredEnv.Spec.ServiceConfig.Env = append(desiredEnv.Spec.ServiceConfig.Env, divergeiov1alpha1.EnvVar{
							Name:  k,
							Value: v,
						})
					}
					// TODO(v1.1): Execute res.SetupSQL here via a Job or direct DB connection

					// Update the child environment with the new env vars
					if err := r.Update(ctx, desiredEnv); err != nil {
						logger.Error(err, "failed to update child Environment with database env vars")
					}
				}
			}
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
		if err := r.Notifier.UpdateGroupStatus(ctx, &pg); err != nil {
			logger.Error(err, "failed to update group status notification")
		}

		prevPhase := statusBase.Status.Phase
		if pg.Status.Phase != prevPhase {
			switch pg.Status.Phase {
			case divergeiov1alpha1.PreviewGroupPhaseRunning:
				if err := r.Notifier.PostGroupReady(ctx, &pg); err != nil {
					logger.Error(err, "failed to post group ready notification")
				}
			case divergeiov1alpha1.PreviewGroupPhaseFailed, divergeiov1alpha1.PreviewGroupPhaseDegraded:
				if err := r.Notifier.PostGroupFailed(ctx, &pg, readyCondition.Reason); err != nil {
					logger.Error(err, "failed to post group failed notification")
				}
			}
		}
	}

	if requeueAfter > 0 {
		return ctrl.Result{Requeue: requeue, RequeueAfter: requeueAfter}, nil
	}
	return ctrl.Result{Requeue: requeue}, nil
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
				if err := r.DatabaseProvider.Teardown(ctx, child); err != nil {
					logger.Error(err, "failed to teardown database for child Environment", "name", child.Name)
				}
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
		if err := r.Notifier.PostGroupTeardown(ctx, pg); err != nil {
			logger.Error(err, "failed to post group teardown notification")
		}
	}

	controllerutil.RemoveFinalizer(pg, previewGroupFinalizer)
	if err := r.Update(ctx, pg); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}

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
				Mode:      "delta",
				Namespace: "same",
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
		client.MatchingLabels{
			labelPreviewGroup:              pg.Name,
			"app.kubernetes.io/managed-by": "diverge",
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
