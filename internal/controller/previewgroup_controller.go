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
	"github.com/divergedev/diverge/internal/multicluster"
	"github.com/divergedev/diverge/internal/notifier"
	"github.com/divergedev/diverge/internal/previewgroup"
	"github.com/divergedev/diverge/pkg/database"
)

const (
	previewGroupFinalizer    = "divergedev.com/previewgroup-protection"
	labelPreviewGroup        = "divergedev.com/previewgroup"
	labelManagedBy           = "divergedev.com/managed-by"
	annotationCommentSource  = "divergedev.com/comment-source"
	annotationReportedCommit = "divergedev.com/reported-commit"
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
	SetupRunner      SetupRunner
	ClusterClients   multicluster.Resolver
	EnableGAMMA      bool // Enable GAMMA mesh routing (requires Istio Ambient)
}

// +kubebuilder:rbac:groups=divergedev.com,resources=previewgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=divergedev.com,resources=previewgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=divergedev.com,resources=previewgroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=divergedev.com,resources=environments,verbs=get;list;watch;create;update;patch;delete
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

	// Set TTL expiry anchored to max(CreatedAt, annotation["divergedev.com/ttl-renewed-at"])
	var requeueAfter time.Duration
	if pg.Spec.Lifecycle != nil && pg.Spec.Lifecycle.TTL != nil && pg.Status.CreatedAt != nil {
		anchorTime := previewgroup.EffectiveTTLAnchor(pg.Status.CreatedAt, pg.Annotations)
		expiryTime := anchorTime.Add(pg.Spec.Lifecycle.TTL.Duration)
		pg.Status.ExpiresAt = &metav1.Time{Time: expiryTime}
		if time.Now().After(expiryTime) {
			logger.Info("PreviewGroup TTL expired, triggering conditional deletion")
			var delOpts []client.DeleteOption
			if pg.UID != "" || pg.ResourceVersion != "" {
				precond := &metav1.Preconditions{}
				if pg.UID != "" {
					uid := pg.UID
					precond.UID = &uid
				}
				if pg.ResourceVersion != "" {
					rv := pg.ResourceVersion
					precond.ResourceVersion = &rv
				}
				delOpts = append(delOpts, &client.DeleteOptions{Preconditions: precond})
			}
			if err := r.Delete(ctx, &pg, delOpts...); err != nil {
				if apierrors.IsConflict(err) {
					// Concurrent update (e.g. ttl-renewed-at join) arrived; re-evaluate before removing routes.
					return ctrl.Result{RequeueAfter: 100 * time.Millisecond}, nil
				}
				if !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("failed to delete expired PreviewGroup: %w", err)
				}
			}
			if err := r.cleanupRoutesAndEndpoints(ctx, pg.Name); err != nil {
				return ctrl.Result{}, err
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
			if err := r.Status().Patch(ctx, &pg, client.MergeFromWithOptions(statusBase, client.MergeFromWithOptimisticLock{})); err != nil {
				if apierrors.IsConflict(err) {
					return ctrl.Result{RequeueAfter: 100 * time.Millisecond}, nil
				}
				return ctrl.Result{}, fmt.Errorf("failed to update PreviewGroup status: %w", err)
			}

			var delOpts []client.DeleteOption
			if pg.UID != "" || pg.ResourceVersion != "" {
				precond := &metav1.Preconditions{}
				if pg.UID != "" {
					uid := pg.UID
					precond.UID = &uid
				}
				if pg.ResourceVersion != "" {
					rv := pg.ResourceVersion
					precond.ResourceVersion = &rv
				}
				delOpts = append(delOpts, &client.DeleteOptions{Preconditions: precond})
			}
			if err := r.Delete(ctx, &pg, delOpts...); err != nil {
				if apierrors.IsConflict(err) {
					return ctrl.Result{RequeueAfter: 100 * time.Millisecond}, nil
				}
				if !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("failed to delete abandoned PreviewGroup: %w", err)
				}
			}
			if err := r.cleanupRoutesAndEndpoints(ctx, pg.Name); err != nil {
				return ctrl.Result{}, err
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

	// Provision a single shared group-level database when Spec.Database is configured on a concrete provider
	groupDBVars, groupDBAnchorNS, groupDBHandled, groupDBReady, groupDBNeedsRequeue, groupDBErr := r.reconcileGroupDatabase(ctx, &pg)
	// Always refresh statusBase ResourceVersion/annotations in case reconcileGroupDatabase persisted or cleared metadata
	statusBase.ResourceVersion = pg.ResourceVersion
	statusBase.Annotations = pg.DeepCopy().Annotations
	var shortRequeue time.Duration
	if groupDBNeedsRequeue {
		shortRequeue = 3 * time.Second
	}
	if groupDBHandled {
		if groupDBErr != nil {
			prevPhase := statusBase.Status.Phase
			pg.Status.Phase = divergeiov1alpha1.PreviewGroupPhaseFailed
			failMsg := fmt.Sprintf("Shared group database provisioning failed: %v", groupDBErr)
			meta.SetStatusCondition(&pg.Status.Conditions, metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "GroupDatabaseFailed",
				Message:            failMsg,
				ObservedGeneration: pg.Generation,
			})
			prevReportedCommit := ""
			if pg.Annotations != nil {
				prevReportedCommit = strings.TrimSpace(pg.Annotations[annotationReportedCommit])
			}
			failCommitKey := commitStateKey(&pg, "failed")
			if r.StatusReporter != nil && failCommitKey != "" && failCommitKey != prevReportedCommit {
				sCtx, cancelS := context.WithTimeout(ctx, 15*time.Second)
				sErr := r.StatusReporter.PostCommitStatus(sCtx, &divergeiov1alpha1.Environment{
					ObjectMeta: metav1.ObjectMeta{Name: pg.Name},
					Spec:       divergeiov1alpha1.EnvironmentSpec{Source: pg.Spec.Source},
					Status:     divergeiov1alpha1.EnvironmentStatus{URL: pg.Spec.Routing.ExternalURL},
				}, "failed", failMsg)
				cancelS()
				if sErr == nil {
					metaBase := pg.DeepCopy()
					if pg.Annotations == nil {
						pg.Annotations = make(map[string]string)
					}
					pg.Annotations[annotationReportedCommit] = failCommitKey
					desiredStatus := pg.Status.DeepCopy()
					if mErr := r.Patch(ctx, &pg, client.MergeFrom(metaBase)); mErr == nil {
						pg.Status = *desiredStatus
						statusBase.ResourceVersion = pg.ResourceVersion
						statusBase.Annotations = pg.DeepCopy().Annotations
					}
				}
			}
			currentCommentSource, sourcePromoted := resetCommentIfSourcePromoted(&pg)
			if r.Notifier != nil && (pg.Status.Phase != prevPhase || sourcePromoted) {
				notifyCtx, cancelN := context.WithTimeout(ctx, 15*time.Second)
				_ = r.Notifier.PostGroupFailed(notifyCtx, &pg, "GroupDatabaseFailed")
				cancelN()
			}
			if pg.Status.CommentID != 0 {
				if currentCommentSource != "" {
					pg.Status.CommentSource = currentCommentSource
				}
			} else {
				pg.Status.CommentSource = ""
			}
			_ = r.Status().Patch(ctx, &pg, client.MergeFrom(statusBase))
			return ctrl.Result{}, groupDBErr
		}
		if !groupDBReady {
			pg.Status.Phase = divergeiov1alpha1.PreviewGroupPhaseDeploying
			meta.SetStatusCondition(&pg.Status.Conditions, metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "GroupDatabaseProvisioning",
				Message:            "Shared group database is provisioning",
				ObservedGeneration: pg.Generation,
			})
			if err := r.Status().Patch(ctx, &pg, client.MergeFrom(statusBase)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update PreviewGroup status: %w", err)
			}
			return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
		}
	}

	servicePhases := make(map[string]divergeiov1alpha1.EnvironmentPhase, len(pg.Spec.Services))
	var childSecretErrs []error

	for _, svc := range pg.Spec.Services {
		envName := childEnvironmentName(pg.Name, svc.Name)

		// Determine target namespace — use service's namespace or default
		targetNS := svc.Namespace
		if targetNS == "" {
			targetNS = "default"
		}
		desiredEnvNames[targetNS+"/"+envName] = true

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
			svcStatus.ChangedServices = []string{}
			servicePhases[svc.Name] = svcStatus.Phase
			serviceStatuses = append(serviceStatuses, svcStatus)
			continue
		}

		// Look up existing child Environment FIRST so immutable clusterRef validation runs BEFORE writing spoke DB Secrets
		var existingEnv divergeiov1alpha1.Environment
		getErr := r.Get(ctx, types.NamespacedName{Name: envName, Namespace: targetNS}, &existingEnv)
		if getErr != nil && !apierrors.IsNotFound(getErr) {
			return ctrl.Result{}, fmt.Errorf("failed to get child Environment %s/%s: %w", targetNS, envName, getErr)
		}
		childExists := getErr == nil
		if childExists {
			var desiredClusterRef *divergeiov1alpha1.ClusterTargetRef
			if pg.Spec.ClusterRef != nil {
				desiredClusterRef = pg.Spec.ClusterRef
			}
			if svc.ClusterRef != nil {
				desiredClusterRef = svc.ClusterRef
			}
			if strings.TrimSpace(existingEnv.Status.TargetCluster) != "" &&
				!divergeiov1alpha1.EqualClusterTargetRef(existingEnv.Spec.Deploy.ClusterRef, desiredClusterRef, targetNS) {
				msg := fmt.Sprintf("effective clusterRef for service %q cannot be changed once provisioned on %q", svc.Name, existingEnv.Status.TargetCluster)
				r.Recorder.Event(&pg, "Warning", "ClusterRefImmutable", msg)
				svcStatus.Phase = divergeiov1alpha1.PhaseFailed
				svcStatus.Reason = "ClusterRefImmutable"
				svcStatus.Message = msg
				svcStatus.ChangedServices = existingEnv.Spec.Deploy.ChangedServices
				svcStatus.TargetCluster = existingEnv.Status.TargetCluster
				servicePhases[svc.Name] = svcStatus.Phase
				serviceStatuses = append(serviceStatuses, svcStatus)
				continue
			}
		}

		var sharedDBSecretName string
		if groupDBHandled && svc.Database == nil && len(groupDBVars) > 0 {
			secName, secErr := r.ensureChildSharedDBSecret(ctx, &pg, svc, envName, targetNS, groupDBAnchorNS, groupDBVars)
			if secErr != nil {
				svcStatus.Phase = divergeiov1alpha1.PhaseFailed
				svcStatus.Reason = "SharedDatabaseSecretFailed"
				svcStatus.Message = secErr.Error()
				servicePhases[svc.Name] = svcStatus.Phase
				serviceStatuses = append(serviceStatuses, svcStatus)
				childSecretErrs = append(childSecretErrs, secErr)
				continue
			}
			sharedDBSecretName = secName
		}

		// Build the desired child Environment CR (only enable shared DB mode when a Secret reference was created)
		useSharedForChild := groupDBHandled && svc.Database == nil && sharedDBSecretName != ""
		desiredEnv := r.buildChildEnvironmentWithSharedDB(&pg, svc, envName, targetNS, useSharedForChild, sharedDBSecretName)

		childSpecUpdated := false
		if !childExists {
			// Create
			if err := r.Create(ctx, desiredEnv); err != nil {
				if apierrors.IsAlreadyExists(err) {
					// Race condition — requeue
					requeue = true
					svcStatus.Phase = divergeiov1alpha1.PhasePending
					svcStatus.Message = "Environment already exists, resyncing"
					svcStatus.Reason = "CreateConflict"
					servicePhases[svc.Name] = svcStatus.Phase
					serviceStatuses = append(serviceStatuses, svcStatus)
					continue
				}
				svcStatus.Phase = divergeiov1alpha1.PhaseFailed
				svcStatus.Message = fmt.Sprintf("Failed to create Environment: %v", err)
				svcStatus.Reason = "CreateFailed"
				servicePhases[svc.Name] = svcStatus.Phase
				serviceStatuses = append(serviceStatuses, svcStatus)
				continue
			}
			svcStatus.Phase = divergeiov1alpha1.PhasePending
			svcStatus.Message = "Environment created"
			svcStatus.ChangedServices = desiredEnv.Spec.Deploy.ChangedServices
			svcStatus.TargetCluster = multicluster.ClusterName(desiredEnv)
			existingEnv = *desiredEnv
		} else {
			// Update — sync spec if changed
			if r.needsUpdate(&existingEnv, desiredEnv) {
				specChanged := !equality.Semantic.DeepEqual(existingEnv.Spec, desiredEnv.Spec)
				existingEnv.Spec = desiredEnv.Spec
				existingEnv.Labels = desiredEnv.Labels
				if existingEnv.Annotations == nil && len(desiredEnv.Annotations) > 0 {
					existingEnv.Annotations = make(map[string]string)
				}
				if tp := desiredEnv.Annotations["divergedev.com/traceparent"]; tp != "" {
					existingEnv.Annotations["divergedev.com/traceparent"] = tp
				}
				if renewed := desiredEnv.Annotations[previewgroup.AnnotationTTLRenewedAt]; renewed != "" {
					existingEnv.Annotations[previewgroup.AnnotationTTLRenewedAt] = renewed
				} else if existingEnv.Annotations != nil {
					delete(existingEnv.Annotations, previewgroup.AnnotationTTLRenewedAt)
				}
				if desiredEnv.Annotations[annotationSharedGroupDB] == "true" {
					existingEnv.Annotations[annotationSharedGroupDB] = "true"
				} else if existingEnv.Annotations != nil {
					delete(existingEnv.Annotations, annotationSharedGroupDB)
				}
				if err := r.Update(ctx, &existingEnv); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to update child Environment %s/%s: %w", targetNS, envName, err)
				}
				childSpecUpdated = specChanged
				r.Recorder.Eventf(&pg, "Normal", "ChildUpdated",
					"Updated Environment %s/%s for service %s", targetNS, envName, svc.Name)
			}
			// Copy status from child
			svcStatus.Phase = existingEnv.Status.Phase
			svcStatus.URL = existingEnv.Status.URL
			svcStatus.ChangedServices = existingEnv.Spec.Deploy.ChangedServices
			svcStatus.TargetCluster = multicluster.ClusterName(&existingEnv)
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

		handoffPhase := svcStatus.Phase
		if childExists && handoffPhase == divergeiov1alpha1.PhaseRunning &&
			(childSpecUpdated ||
				(existingEnv.Generation > 0 && existingEnv.Status.ObservedGeneration < existingEnv.Generation) ||
				meta.IsStatusConditionFalse(existingEnv.Status.Conditions, "DatabaseReady")) {
			handoffPhase = divergeiov1alpha1.PhaseDeploying
			svcStatus.Phase = divergeiov1alpha1.PhaseDeploying
			if svcStatus.Reason == "" {
				svcStatus.Reason = "ChildSyncing"
			}
			if svcStatus.Message == "" {
				svcStatus.Message = "Waiting for child Environment to reconcile current specification"
			}
			requeue = true
		}
		servicePhases[svc.Name] = handoffPhase
		serviceStatuses = append(serviceStatuses, svcStatus)
	}

	// Reconcile standalone PR Environment hibernation/waking for attached/detached group members
	memberSyncErr := r.syncMemberStandaloneEnvironments(ctx, &pg, servicePhases)
	if memberSyncErr != nil {
		logger.Error(memberSyncErr, "failed to reconcile standalone PR Environment hibernation/wake")
	}

	// Delete orphaned Environments (services removed from spec)
	orphanCleanupRequeue, orphanCleanupErr := r.deleteOrphanedEnvironments(ctx, &pg, desiredEnvNames)
	if orphanCleanupRequeue && (shortRequeue == 0 || 3*time.Second < shortRequeue) {
		shortRequeue = 3 * time.Second
	}
	if orphanCleanupErr != nil {
		logger.Error(orphanCleanupErr, "failed to delete orphaned Environments")
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

	prevPhase := statusBase.Status.Phase
	currentCommentSource, sourcePromoted := resetCommentIfSourcePromoted(&pg)

	prevReportedCommit := ""
	if pg.Annotations != nil {
		prevReportedCommit = strings.TrimSpace(pg.Annotations[annotationReportedCommit])
	}
	var commitState, commitDesc string
	switch pg.Status.Phase {
	case divergeiov1alpha1.PreviewGroupPhaseRunning:
		commitState = "success"
		commitDesc = fmt.Sprintf("Preview group %s ready", pg.Name)
	case divergeiov1alpha1.PreviewGroupPhaseFailed, divergeiov1alpha1.PreviewGroupPhaseDegraded:
		commitState = "failed"
		commitDesc = readyCondition.Message
	}
	currentCommitStateKey := commitStateKey(&pg, commitState)
	reportedCommitUpdated := false

	if r.StatusReporter != nil && currentCommitStateKey != "" && currentCommitStateKey != prevReportedCommit {
		syntheticEnv := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: pg.Name},
			Spec:       divergeiov1alpha1.EnvironmentSpec{Source: pg.Spec.Source},
			Status:     divergeiov1alpha1.EnvironmentStatus{URL: pg.Spec.Routing.ExternalURL},
		}
		sCtx, cancelS := context.WithTimeout(ctx, 15*time.Second)
		if sErr := r.StatusReporter.PostCommitStatus(sCtx, syntheticEnv, commitState, commitDesc); sErr != nil {
			logger.Error(sErr, "failed to post preview group commit status")
			requeue = true
		} else {
			reportedCommitUpdated = true
		}
		cancelS()
	}

	// Run SCM notifications BEFORE Status().Patch so newly created Status.CommentID and Status.CommentSource are persisted atomically together,
	// and only emit notifications when phase, service statuses, or promoted source MR changed.
	if r.Notifier != nil {
		servicesChanged := !equality.Semantic.DeepEqual(statusBase.Status.Services, pg.Status.Services)
		if pg.Status.Phase != prevPhase || sourcePromoted {
			notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			switch pg.Status.Phase {
			case divergeiov1alpha1.PreviewGroupPhaseRunning:
				if err := r.Notifier.PostGroupReady(notifyCtx, &pg); err != nil {
					logger.Error(err, "failed to post group ready notification")
				}
			case divergeiov1alpha1.PreviewGroupPhaseFailed, divergeiov1alpha1.PreviewGroupPhaseDegraded:
				if err := r.Notifier.PostGroupFailed(notifyCtx, &pg, readyCondition.Reason); err != nil {
					logger.Error(err, "failed to post group failed notification")
				}
			default:
				if pg.Status.CommentID == 0 {
					if err := r.Notifier.PostGroupCreated(notifyCtx, &pg); err != nil {
						logger.Error(err, "failed to post group created notification")
					}
				} else {
					if err := r.Notifier.UpdateGroupStatus(notifyCtx, &pg); err != nil {
						logger.Error(err, "failed to update group status notification")
					}
				}
			}
			cancel()
		} else if servicesChanged {
			notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			if err := r.Notifier.UpdateGroupStatus(notifyCtx, &pg); err != nil {
				logger.Error(err, "failed to update group status notification")
			}
			cancel()
		}
	}

	if pg.Status.CommentID != 0 {
		if currentCommentSource != "" {
			pg.Status.CommentSource = currentCommentSource
		}
	} else {
		pg.Status.CommentSource = ""
	}

	// Persist reported-commit metadata separately when updated; CommentID + CommentSource are persisted atomically in Status().Patch below.
	if reportedCommitUpdated && prevReportedCommit != currentCommitStateKey {
		desiredStatus := pg.Status.DeepCopy()
		metaBase := pg.DeepCopy()
		if pg.Annotations == nil {
			pg.Annotations = make(map[string]string)
		}
		pg.Annotations[annotationReportedCommit] = currentCommitStateKey
		if mErr := r.Patch(ctx, &pg, client.MergeFrom(metaBase)); mErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to persist PreviewGroup notification metadata: %w", mErr)
		}
		pg.Status = *desiredStatus
		statusBase.ResourceVersion = pg.ResourceVersion
		statusBase.Annotations = pg.DeepCopy().Annotations
	}

	// Persist status (including CommentID + CommentSource together atomically)
	patch := client.MergeFrom(statusBase)
	if err := r.Status().Patch(ctx, &pg, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update PreviewGroup status: %w", err)
	}

	if orphanCleanupErr != nil {
		return ctrl.Result{}, orphanCleanupErr
	}
	if memberSyncErr != nil {
		return ctrl.Result{}, memberSyncErr
	}
	if len(childSecretErrs) > 0 {
		return ctrl.Result{}, errors.Join(childSecretErrs...)
	}
	if requeue && (shortRequeue == 0 || 1*time.Second < shortRequeue) {
		shortRequeue = 1 * time.Second
	}
	if shortRequeue > 0 && (requeueAfter == 0 || shortRequeue < requeueAfter) {
		requeueAfter = shortRequeue
	}
	if requeueAfter > 0 {
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
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

	if err := r.cleanupRoutesAndEndpoints(ctx, pg.Name); err != nil {
		return ctrl.Result{}, err
	}

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
			hookCtx, cancelHook := context.WithTimeout(ctx, 15*time.Second)
			hookErr := r.cleanupPostDeployJobs(hookCtx, child)
			cancelHook()
			if hookErr != nil {
				if !shouldBypassPreviewGroupHookCleanup(pg, child) {
					logger.Error(hookErr, "failed to cleanup post-deploy jobs for child Environment", "name", child.Name)
					remaining++
					continue
				}
				logger.Info("Bypassing post-deploy hook cleanup failure under force-teardown policy", "child", child.Name, "error", hookErr)
				if r.Recorder != nil {
					r.Recorder.Event(pg, "Warning", "ForceTeardown", "Bypassed post-deploy hook cleanup failure after timeout or force-teardown annotation")
				}
				r.propagateForceTeardownToChild(ctx, child)
			}

			// When child has environmentFinalizer on a real cluster, EnvironmentReconciler.handleTeardown
			// drains pods first and tears down per-service DBs dynamically. Only run fallback Teardown here
			// when child has no finalizer (e.g. standalone PreviewGroup controller unit tests).
			if r.DatabaseProvider != nil && child.Spec.Database.Mode != "" &&
				child.Annotations[annotationSharedGroupDB] != "true" &&
				!controllerutil.ContainsFinalizer(child, environmentFinalizer) {
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

	// All child Environments have drained — tear down any shared group-level database and wake attached standalone PR Environments
	if err := r.teardownGroupDatabase(ctx, pg); err != nil {
		logger.Error(err, "failed to teardown shared group database", "group", pg.Name)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, err
	}
	if err := r.wakeUnclaimedStandaloneEnvironments(ctx, pg.Name, nil); err != nil {
		logger.Error(err, "failed to wake standalone PR Environments during teardown", "group", pg.Name)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, err
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
	return r.buildChildEnvironmentWithSharedDB(pg, svc, envName, targetNS, false, "")
}

func (r *PreviewGroupReconciler) buildChildEnvironmentWithSharedDB(
	pg *divergeiov1alpha1.PreviewGroup,
	svc divergeiov1alpha1.PreviewGroupServiceSpec,
	envName, targetNS string,
	useSharedGroupDB bool,
	sharedDBSecretName string,
) *divergeiov1alpha1.Environment {
	// Determine database config — service-level overrides group-level.
	// When useSharedGroupDB is true, PreviewGroupReconciler already provisioned the single shared database
	// (and ran any group-level MigrationJob/Atlas once) and populated sharedDBSecretName in targetNS.
	dbConfig := divergeiov1alpha1.EnvironmentDatabase{}
	if useSharedGroupDB {
		dbConfig.Mode = "shared"
		dbConfig.ConnectionRef = sharedDBSecretName
	} else {
		if pg.Spec.Database != nil {
			dbConfig = *pg.Spec.Database
		}
		if svc.Database != nil {
			dbConfig = *svc.Database
		}
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

	var clusterRef *divergeiov1alpha1.ClusterTargetRef
	if pg.Spec.ClusterRef != nil {
		clusterRef = pg.Spec.ClusterRef.DeepCopy()
	}
	if svc.ClusterRef != nil {
		clusterRef = svc.ClusterRef.DeepCopy()
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
				ClusterRef:      clusterRef,
			},
		},
	}

	if tp := pg.Annotations["divergedev.com/traceparent"]; tp != "" {
		if env.Annotations == nil {
			env.Annotations = make(map[string]string)
		}
		env.Annotations["divergedev.com/traceparent"] = tp
	}
	if renewed := pg.Annotations[previewgroup.AnnotationTTLRenewedAt]; renewed != "" {
		if env.Annotations == nil {
			env.Annotations = make(map[string]string)
		}
		env.Annotations[previewgroup.AnnotationTTLRenewedAt] = renewed
	}
	if useSharedGroupDB {
		if env.Annotations == nil {
			env.Annotations = make(map[string]string)
		}
		env.Annotations[annotationSharedGroupDB] = "true"
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
	if tp := desired.Annotations["divergedev.com/traceparent"]; tp != "" && existing.Annotations["divergedev.com/traceparent"] != tp {
		return true
	}
	if existing.Annotations[previewgroup.AnnotationTTLRenewedAt] != desired.Annotations[previewgroup.AnnotationTTLRenewedAt] {
		return true
	}
	if (desired.Annotations[annotationSharedGroupDB] == "true") != (existing.Annotations[annotationSharedGroupDB] == "true") {
		return true
	}
	return false
}

func commitStateKey(pg *divergeiov1alpha1.PreviewGroup, state string) string {
	if pg == nil || strings.TrimSpace(state) == "" {
		return ""
	}
	sha := strings.TrimSpace(pg.Spec.Source.CommitSHA)
	if sha == "" {
		return ""
	}
	return fmt.Sprintf("%s:%s@%s:%s",
		strings.ToLower(strings.TrimSpace(pg.Spec.Source.Provider)),
		strings.ToLower(strings.TrimSpace(pg.Spec.Source.Project)),
		sha,
		strings.ToLower(strings.TrimSpace(state)),
	)
}

func previewGroupCommentSourceKey(pg *divergeiov1alpha1.PreviewGroup) string {
	if pg == nil || pg.Spec.Source.MR <= 0 {
		return ""
	}
	return fmt.Sprintf("%s:%s#%d",
		strings.ToLower(strings.TrimSpace(pg.Spec.Source.Provider)),
		strings.ToLower(strings.TrimSpace(pg.Spec.Source.Project)),
		pg.Spec.Source.MR,
	)
}

func resetCommentIfSourcePromoted(pg *divergeiov1alpha1.PreviewGroup) (string, bool) {
	if pg == nil {
		return "", false
	}
	currentCommentSource := previewGroupCommentSourceKey(pg)
	prevCommentSource := strings.TrimSpace(pg.Status.CommentSource)
	if prevCommentSource == "" && pg.Annotations != nil {
		prevCommentSource = strings.TrimSpace(pg.Annotations[annotationCommentSource])
	}
	if pg.Status.CommentID != 0 && prevCommentSource != "" && prevCommentSource != currentCommentSource {
		// Creator MR left and Spec.Source promoted to a new MR (potentially in the same monorepo):
		// reset CommentID and CommentSource so we post a fresh comment on the promoted MR instead of editing the closed PR's comment.
		pg.Status.CommentID = 0
		pg.Status.CommentSource = ""
		return currentCommentSource, true
	}
	return currentCommentSource, false
}

// listChildEnvironments returns all Environments owned by this PreviewGroup.
func (r *PreviewGroupReconciler) listChildEnvironments(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) ([]divergeiov1alpha1.Environment, error) {
	var envList divergeiov1alpha1.EnvironmentList
	opts := []client.ListOption{
		client.MatchingLabels{
			labelPreviewGroup: pg.Name,
			labelManagedBy:    "diverge-previewgroup",
		},
	}
	if pg.Namespace != "" {
		opts = append(opts, client.InNamespace(pg.Namespace))
	}
	if err := r.List(ctx, &envList, opts...); err != nil {
		return nil, err
	}
	return envList.Items, nil
}

func shouldBypassPreviewGroupHookCleanup(pg *divergeiov1alpha1.PreviewGroup, child *divergeiov1alpha1.Environment) bool {
	if pg != nil {
		if pg.Annotations["divergedev.com/force-teardown"] == "true" {
			return true
		}
		if !pg.DeletionTimestamp.IsZero() && time.Since(pg.DeletionTimestamp.Time) > 15*time.Minute {
			return true
		}
	}
	if child != nil {
		if child.Annotations["divergedev.com/force-teardown"] == "true" {
			return true
		}
		if !child.DeletionTimestamp.IsZero() && time.Since(child.DeletionTimestamp.Time) > 15*time.Minute {
			return true
		}
	}
	return false
}

func (r *PreviewGroupReconciler) propagateForceTeardownToChild(ctx context.Context, child *divergeiov1alpha1.Environment) {
	if child == nil || child.Annotations["divergedev.com/force-teardown"] == "true" {
		return
	}
	base := child.DeepCopy()
	if child.Annotations == nil {
		child.Annotations = make(map[string]string)
	}
	child.Annotations["divergedev.com/force-teardown"] = "true"
	patchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = r.Patch(patchCtx, child, client.MergeFrom(base))
}

// deleteOrphanedEnvironments removes child Environments that are no longer in the spec.
func (r *PreviewGroupReconciler) deleteOrphanedEnvironments(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup, desired map[string]bool) (bool, error) {
	logger := log.FromContext(ctx)

	children, err := r.listChildEnvironments(ctx, pg)
	if err != nil {
		return false, err
	}

	var errs []error
	for i := range children {
		child := &children[i]
		if !desired[child.Namespace+"/"+child.Name] && !desired[child.Name] {
			logger.Info("Deleting orphaned Environment", "name", child.Name, "namespace", child.Namespace)
			hookCtx, cancelHook := context.WithTimeout(ctx, 15*time.Second)
			hookErr := r.cleanupPostDeployJobs(hookCtx, child)
			cancelHook()
			if hookErr != nil {
				if !shouldBypassPreviewGroupHookCleanup(pg, child) {
					errs = append(errs, fmt.Errorf("failed to cleanup post-deploy jobs for orphaned Environment %s/%s: %w", child.Namespace, child.Name, hookErr))
					continue
				}
				logger.Info("Bypassing orphan post-deploy hook cleanup failure under force-teardown policy", "child", child.Name, "error", hookErr)
				if r.Recorder != nil {
					r.Recorder.Event(pg, "Warning", "ForceTeardown", "Bypassed orphan post-deploy hook cleanup failure under force-teardown policy")
				}
				r.propagateForceTeardownToChild(ctx, child)
			}
			// When child has environmentFinalizer on a real cluster, EnvironmentReconciler.handleTeardown
			// drains pods first and tears down per-service DBs dynamically. Only run fallback Teardown here
			// when child has no finalizer (e.g. standalone PreviewGroup controller unit tests).
			if r.DatabaseProvider != nil && child.Spec.Database.Mode != "" &&
				child.Annotations[annotationSharedGroupDB] != "true" &&
				!controllerutil.ContainsFinalizer(child, environmentFinalizer) {
				dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				if err := r.DatabaseProvider.Teardown(dbCtx, child); err != nil {
					logger.Error(err, "failed to teardown database for child Environment", "name", child.Name)
				}
				cancel()
			}

			if err := r.Delete(ctx, child); err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete orphaned Environment %s/%s: %w", child.Namespace, child.Name, err))
				continue
			}
			r.Recorder.Eventf(pg, "Normal", "OrphanDeleted",
				"Deleted orphaned Environment %s/%s", child.Namespace, child.Name)
		}
	}
	hasPinned := pg != nil && pg.Annotations != nil &&
		(strings.TrimSpace(pg.Annotations[annotationGroupDBNamespace]) != "" ||
			strings.TrimSpace(pg.Annotations[annotationGroupDBProvider]) != "")
	secRequeue := false
	if hasDedicatedGroupDatabase(pg) || hasPinned {
		var secErr error
		secRequeue, secErr = r.cleanupOrphanedChildSharedDBSecrets(ctx, pg, desired)
		if secErr != nil {
			errs = append(errs, secErr)
		}
	}
	return secRequeue, errors.Join(errs...)
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
	case failed > 0:
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
	prefix := strings.TrimRight(raw[:63-9], "-")
	if prefix == "" {
		prefix = "pg"
	}
	return prefix + "-" + hash
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
		if err := r.List(cleanupCtx, &httpRouteList, client.MatchingLabels{labelPreviewGroup: pgName}); err != nil {
			if meta.IsNoMatchError(err) {
				return nil
			}
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
		grpcRouteList.SetAPIVersion("gateway.networking.k8s.io/v1")
		grpcRouteList.SetKind("GRPCRouteList")
		if err := r.List(cleanupCtx, &grpcRouteList, client.MatchingLabels{labelPreviewGroup: pgName}); err != nil {
			if meta.IsNoMatchError(err) {
				return nil
			}
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
			labelPreviewGroup:                        pgName,
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
