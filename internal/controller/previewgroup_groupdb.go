package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/database/cnpg"
	"github.com/divergedev/diverge/internal/metrics"
	"github.com/divergedev/diverge/internal/multicluster"
	"github.com/divergedev/diverge/internal/previewgroup"
)

const (
	annotationGroupDBNamespace     = "divergedev.com/group-db-namespace"
	annotationGroupDBProvider      = "divergedev.com/group-db-provider"
	annotationGroupDBMigrationHash = "divergedev.com/group-db-migration-hash"
)

var (
	pgDBNonAlnum  = regexp.MustCompile(`[^a-z0-9-]`)
	pgDBMultiDash = regexp.MustCompile(`-{2,}`)
)

func hasDedicatedGroupDatabase(pg *divergeiov1alpha1.PreviewGroup) bool {
	if pg == nil || pg.Spec.Database == nil {
		return false
	}
	isSharedGroupOptIn := pg.Labels[previewgroup.LabelComposedGroup] == "true" ||
		pg.Annotations[annotationSharedGroupDB] == "true"
	if !isSharedGroupOptIn {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(pg.Spec.Database.Mode))
	return mode != "" && mode != "shared" && mode != "none" && mode != "noop"
}

func groupDatabaseAnchorName(groupName string) string {
	raw := strings.ToLower("pgdb-" + strings.TrimSpace(groupName))
	raw = strings.NewReplacer(".", "-", "_", "-").Replace(raw)
	raw = pgDBNonAlnum.ReplaceAllString(raw, "")
	raw = pgDBMultiDash.ReplaceAllString(raw, "-")
	raw = strings.Trim(raw, "-")
	if raw == "" {
		raw = "pgdb"
	}
	sum := sha256.Sum256([]byte(groupName))
	suffix := fmt.Sprintf("%x", sum)[:8]
	const maxLen = 45
	if len(raw)+1+len(suffix) <= maxLen {
		return raw + "-" + suffix
	}
	prefix := strings.TrimRight(raw[:maxLen-1-len(suffix)], "-")
	if prefix == "" {
		prefix = "pgdb"
	}
	return prefix + "-" + suffix
}

func resolveGroupDatabaseAnchorNamespace(pg *divergeiov1alpha1.PreviewGroup) string {
	if pg != nil && pg.Annotations != nil {
		if pinned := strings.TrimSpace(pg.Annotations[annotationGroupDBNamespace]); pinned != "" {
			return pinned
		}
	}
	if pg != nil {
		for _, svc := range pg.Spec.Services {
			if svc.Mode == divergeiov1alpha1.ServiceModeBaseline {
				continue
			}
			if ns := strings.TrimSpace(svc.Namespace); ns != "" {
				return ns
			}
			return "default"
		}
		if strings.TrimSpace(pg.Namespace) != "" {
			return strings.TrimSpace(pg.Namespace)
		}
	}
	return "default"
}

func (r *PreviewGroupReconciler) buildGroupDatabaseAnchor(pg *divergeiov1alpha1.PreviewGroup, anchorNS string) *divergeiov1alpha1.Environment {
	var dbSpec divergeiov1alpha1.EnvironmentDatabase
	if pg.Spec.Database != nil {
		dbSpec = *pg.Spec.Database.DeepCopy()
	}
	var clusterRef *divergeiov1alpha1.ClusterTargetRef
	if pg.Spec.ClusterRef != nil {
		clusterRef = pg.Spec.ClusterRef.DeepCopy()
	}
	recordedProv := ""
	if pg.Annotations != nil {
		recordedProv = strings.TrimSpace(pg.Annotations[annotationGroupDBProvider])
	}
	var ownerRefs []metav1.OwnerReference
	if pg.UID != "" && pg.Spec.ClusterRef == nil {
		ownerRefs = []metav1.OwnerReference{
			{
				APIVersion: divergeiov1alpha1.GroupVersion.String(),
				Kind:       "PreviewGroup",
				Name:       pg.Name,
				UID:        pg.UID,
			},
		}
	}
	return &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            groupDatabaseAnchorName(pg.Name),
			Namespace:       anchorNS,
			OwnerReferences: ownerRefs,
			Labels: map[string]string{
				labelPreviewGroup: pg.Name,
				labelManagedBy:    "diverge-previewgroup",
			},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source:   pg.Spec.Source,
			Database: dbSpec,
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Mode:       "delta",
				Namespace:  "same",
				ClusterRef: clusterRef,
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			DatabaseProvider: recordedProv,
		},
	}
}

func (r *PreviewGroupReconciler) envProvisionHelper() *EnvironmentReconciler {
	return &EnvironmentReconciler{
		Client:           r.Client,
		Scheme:           r.Scheme,
		DatabaseProvider: r.DatabaseProvider,
		ClusterClients:   r.ClusterClients,
		SetupRunner:      r.SetupRunner,
	}
}

func groupDatabaseMigrationDigest(pg *divergeiov1alpha1.PreviewGroup, anchorEnvName, anchorNS string) string {
	if pg == nil || pg.Spec.Database == nil {
		return ""
	}
	payload, _ := json.Marshal(struct {
		Anchor  string `json:"anchor"`
		NS      string `json:"ns"`
		Mode    string `json:"mode"`
		MigrJob any    `json:"migrJob,omitempty"`
		Atlas   any    `json:"atlas,omitempty"`
	}{
		Anchor:  anchorEnvName,
		NS:      anchorNS,
		Mode:    strings.ToLower(strings.TrimSpace(pg.Spec.Database.Mode)),
		MigrJob: pg.Spec.Database.MigrationJob,
		Atlas:   pg.Spec.Database.Atlas,
	})
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:8])
}

func isGroupMigrationBlocking(db *divergeiov1alpha1.EnvironmentDatabase) bool {
	if db == nil {
		return true
	}
	if db.MigrationJob != nil && db.MigrationJob.Blocking != nil && !*db.MigrationJob.Blocking {
		return false
	}
	if db.Atlas != nil && db.Atlas.Blocking != nil && !*db.Atlas.Blocking {
		return false
	}
	return true
}

func blockingObservingAnchor(anchor *divergeiov1alpha1.Environment) *divergeiov1alpha1.Environment {
	obs := anchor.DeepCopy()
	t := true
	if obs.Spec.Database.MigrationJob != nil {
		obs.Spec.Database.MigrationJob.Blocking = &t
	}
	if obs.Spec.Database.Atlas != nil {
		obs.Spec.Database.Atlas.Blocking = &t
	}
	return obs
}

func childStillUsesSharedDBSecret(child *divergeiov1alpha1.Environment, secName string) bool {
	if child == nil {
		return false
	}
	if child.Annotations[annotationSharedGroupDB] == "true" {
		return true
	}
	if secName != "" && strings.TrimSpace(child.Spec.Database.ConnectionRef) == secName {
		return true
	}
	if child.Generation > 0 {
		if child.Status.ObservedGeneration < child.Generation || child.Status.Phase != divergeiov1alpha1.PhaseRunning {
			return true
		}
		servicesReady := meta.FindStatusCondition(child.Status.Conditions, "ServicesReady")
		if servicesReady == nil || servicesReady.Status != metav1.ConditionTrue || servicesReady.ObservedGeneration < child.Generation {
			return true
		}
	}
	return false
}

var argoRolloutListGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "RolloutList",
}

func workloadBelongsToChild(name string, labels map[string]string, childName string) bool {
	return labels["divergedev.com/environment"] == childName || name == childName
}

func containerRefsSecret(envFrom []corev1.EnvFromSource, env []corev1.EnvVar, secName string) bool {
	for i := range envFrom {
		if envFrom[i].SecretRef != nil && envFrom[i].SecretRef.Name == secName {
			return true
		}
	}
	for i := range env {
		if env[i].ValueFrom != nil && env[i].ValueFrom.SecretKeyRef != nil && env[i].ValueFrom.SecretKeyRef.Name == secName {
			return true
		}
	}
	return false
}

func podSpecReferencesSecret(spec *corev1.PodSpec, secName string) bool {
	if spec == nil || secName == "" {
		return false
	}
	for i := range spec.Containers {
		if containerRefsSecret(spec.Containers[i].EnvFrom, spec.Containers[i].Env, secName) {
			return true
		}
	}
	for i := range spec.InitContainers {
		if containerRefsSecret(spec.InitContainers[i].EnvFrom, spec.InitContainers[i].Env, secName) {
			return true
		}
	}
	for i := range spec.EphemeralContainers {
		if containerRefsSecret(spec.EphemeralContainers[i].EnvFrom, spec.EphemeralContainers[i].Env, secName) {
			return true
		}
	}
	for i := range spec.Volumes {
		vol := &spec.Volumes[i]
		if vol.Secret != nil && vol.Secret.SecretName == secName {
			return true
		}
		if vol.Projected != nil {
			for j := range vol.Projected.Sources {
				if vol.Projected.Sources[j].Secret != nil && vol.Projected.Sources[j].Secret.Name == secName {
					return true
				}
			}
		}
	}
	return false
}

func unstructuredContainersRefSecret(containers []interface{}, secName string) bool {
	for _, item := range containers {
		cMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if envFrom, _, _ := unstructured.NestedSlice(cMap, "envFrom"); len(envFrom) > 0 {
			for _, efItem := range envFrom {
				efMap, ok := efItem.(map[string]interface{})
				if !ok {
					continue
				}
				if sName, _, _ := unstructured.NestedString(efMap, "secretRef", "name"); sName == secName {
					return true
				}
			}
		}
		if env, _, _ := unstructured.NestedSlice(cMap, "env"); len(env) > 0 {
			for _, eItem := range env {
				eMap, ok := eItem.(map[string]interface{})
				if !ok {
					continue
				}
				if sName, _, _ := unstructured.NestedString(eMap, "valueFrom", "secretKeyRef", "name"); sName == secName {
					return true
				}
			}
		}
	}
	return false
}

func unstructuredPodTemplateReferencesSecret(obj map[string]interface{}, secName string) bool {
	if obj == nil || secName == "" {
		return false
	}
	podSpec, found, err := unstructured.NestedMap(obj, "spec", "template", "spec")
	if err != nil || !found || podSpec == nil {
		return false
	}
	for _, key := range []string{"containers", "initContainers", "ephemeralContainers"} {
		if list, ok, _ := unstructured.NestedSlice(podSpec, key); ok && unstructuredContainersRefSecret(list, secName) {
			return true
		}
	}
	volumes, found, _ := unstructured.NestedSlice(podSpec, "volumes")
	if !found {
		return false
	}
	for _, vItem := range volumes {
		vMap, ok := vItem.(map[string]interface{})
		if !ok {
			continue
		}
		if sName, _, _ := unstructured.NestedString(vMap, "secret", "secretName"); sName == secName {
			return true
		}
		sources, _, _ := unstructured.NestedSlice(vMap, "projected", "sources")
		for _, srcItem := range sources {
			srcMap, ok := srcItem.(map[string]interface{})
			if !ok {
				continue
			}
			if sName, _, _ := unstructured.NestedString(srcMap, "secret", "name"); sName == secName {
				return true
			}
		}
	}
	return false
}

func nestedInt64OrString(obj map[string]interface{}, fields ...string) (int64, bool) {
	val, found, err := unstructured.NestedFieldNoCopy(obj, fields...)
	if err != nil || !found || val == nil {
		return 0, false
	}
	switch v := val.(type) {
	case int64:
		return v, true
	case int32:
		return int64(v), true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	case string:
		parsed, pErr := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if pErr != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func isIgnorableWorkloadListErr(err error, allowNotFound bool) bool {
	if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
		return true
	}
	return allowNotFound && apierrors.IsNotFound(err)
}

func rolloutStillUsesOrRollingOut(ro *unstructured.Unstructured, childName, secName string) bool {
	if unstructuredPodTemplateReferencesSecret(ro.Object, secName) {
		return true
	}
	if !workloadBelongsToChild(ro.GetName(), ro.GetLabels(), childName) {
		return false
	}
	specReplicas, hasSpecRepl := nestedInt64OrString(ro.Object, "spec", "replicas")
	if !hasSpecRepl {
		specReplicas = 1
	}
	statusReplicas, _ := nestedInt64OrString(ro.Object, "status", "replicas")
	if specReplicas <= 0 && statusReplicas <= 0 {
		return false
	}
	gen := ro.GetGeneration()
	obsGen, hasObsGen := nestedInt64OrString(ro.Object, "status", "observedGeneration")
	if gen > 0 && (!hasObsGen || obsGen < gen) {
		return true
	}
	phase, _, _ := unstructured.NestedString(ro.Object, "status", "phase")
	if phase != "Healthy" {
		return true
	}
	stableRS, _, _ := unstructured.NestedString(ro.Object, "status", "stableRS")
	currentPodHash, _, _ := unstructured.NestedString(ro.Object, "status", "currentPodHash")
	if stableRS != "" && currentPodHash != "" && stableRS != currentPodHash {
		return true
	}
	updatedReplicas, _ := nestedInt64OrString(ro.Object, "status", "updatedReplicas")
	return updatedReplicas < specReplicas || statusReplicas > updatedReplicas
}

func namespaceWorkloadsStillUseSecret(ctx context.Context, c client.Client, ns, childName, secName string) (bool, error) {
	var podList corev1.PodList
	if err := c.List(ctx, &podList, client.InNamespace(ns)); err != nil {
		if !isIgnorableWorkloadListErr(err, false) {
			return true, fmt.Errorf("failed to list pods in %s for shared DB usage check: %w", ns, err)
		}
	} else {
		for i := range podList.Items {
			pod := &podList.Items[i]
			if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				continue
			}
			if podSpecReferencesSecret(&pod.Spec, secName) {
				return true, nil
			}
		}
	}

	var depList appsv1.DeploymentList
	if err := c.List(ctx, &depList, client.InNamespace(ns)); err != nil {
		if !isIgnorableWorkloadListErr(err, false) {
			return true, fmt.Errorf("failed to list deployments in %s for shared DB usage check: %w", ns, err)
		}
	} else {
		for i := range depList.Items {
			dep := &depList.Items[i]
			if podSpecReferencesSecret(&dep.Spec.Template.Spec, secName) {
				return true, nil
			}
			if !workloadBelongsToChild(dep.Name, dep.Labels, childName) {
				continue
			}
			desired := int32(1)
			if dep.Spec.Replicas != nil {
				desired = *dep.Spec.Replicas
			}
			if (desired > 0 || dep.Status.Replicas > 0) &&
				((dep.Generation > 0 && dep.Status.ObservedGeneration < dep.Generation) ||
					dep.Status.UpdatedReplicas < desired ||
					dep.Status.Replicas > dep.Status.UpdatedReplicas) {
				return true, nil
			}
		}
	}

	var stsList appsv1.StatefulSetList
	if err := c.List(ctx, &stsList, client.InNamespace(ns)); err != nil {
		if !isIgnorableWorkloadListErr(err, false) {
			return true, fmt.Errorf("failed to list statefulsets in %s for shared DB usage check: %w", ns, err)
		}
	} else {
		for i := range stsList.Items {
			sts := &stsList.Items[i]
			if podSpecReferencesSecret(&sts.Spec.Template.Spec, secName) {
				return true, nil
			}
			if !workloadBelongsToChild(sts.Name, sts.Labels, childName) {
				continue
			}
			desired := int32(1)
			if sts.Spec.Replicas != nil {
				desired = *sts.Spec.Replicas
			}
			if (desired > 0 || sts.Status.Replicas > 0) &&
				((sts.Generation > 0 && sts.Status.ObservedGeneration < sts.Generation) ||
					sts.Status.UpdatedReplicas < desired ||
					(sts.Status.UpdateRevision != "" && sts.Status.CurrentRevision != sts.Status.UpdateRevision)) {
				return true, nil
			}
		}
	}

	rolloutList := &unstructured.UnstructuredList{}
	rolloutList.SetGroupVersionKind(argoRolloutListGVK)
	if err := c.List(ctx, rolloutList, client.InNamespace(ns)); err != nil {
		if !isIgnorableWorkloadListErr(err, true) {
			return true, fmt.Errorf("failed to list rollouts in %s for shared DB usage check: %w", ns, err)
		}
	} else {
		for i := range rolloutList.Items {
			if rolloutStillUsesOrRollingOut(&rolloutList.Items[i], childName, secName) {
				return true, nil
			}
		}
	}

	return false, nil
}

// childOrWorkloadStillUsesSharedDBSecret verifies both child Environment status metadata and live
// target-cluster Pods, Deployments, StatefulSets, and Argo Rollouts before allowing shared DB teardown or Secret cleanup.
func (r *PreviewGroupReconciler) childOrWorkloadStillUsesSharedDBSecret(
	ctx context.Context,
	child *divergeiov1alpha1.Environment,
	secName string,
) (bool, error) {
	if childStillUsesSharedDBSecret(child, secName) {
		return true, nil
	}
	if child == nil || secName == "" || r == nil || r.Client == nil {
		return false, nil
	}

	targetClient, _, _, err := r.envProvisionHelper().resolveTargetCluster(ctx, child)
	if err != nil {
		if multicluster.IsRemoteEnv(child) {
			return true, fmt.Errorf("failed to resolve target cluster for child %s/%s shared DB usage check: %w", child.Namespace, child.Name, err)
		}
		targetClient = r.Client
	}
	if targetClient == nil {
		targetClient = r.Client
	}

	targetNS := child.Namespace
	if child.Spec.Deploy.Namespace == "create" {
		targetNS = child.PreviewNamespace()
	}
	seenNS := make(map[string]bool, 2)
	var namespaces []string
	for _, ns := range []string{targetNS, child.Namespace} {
		ns = strings.TrimSpace(ns)
		if ns != "" && !seenNS[ns] {
			seenNS[ns] = true
			namespaces = append(namespaces, ns)
		}
	}

	for _, ns := range namespaces {
		inUse, checkErr := namespaceWorkloadsStillUseSecret(ctx, targetClient, ns, child.Name, secName)
		if checkErr != nil {
			return true, checkErr
		}
		if inUse {
			return true, nil
		}
	}
	return false, nil
}

// reconcileGroupDatabase provisions a single shared database for the PreviewGroup when Spec.Database is configured.
// Returns (sharedEnvVars, anchorNamespace, handledByGroup, ready, requeueNeeded, error).
func (r *PreviewGroupReconciler) reconcileGroupDatabase(
	ctx context.Context,
	pg *divergeiov1alpha1.PreviewGroup,
) (envVars map[string]string, anchorNS string, handled bool, ready bool, requeueNeeded bool, retErr error) {
	if !hasDedicatedGroupDatabase(pg) {
		hasPinned := pg != nil && pg.Annotations != nil &&
			(strings.TrimSpace(pg.Annotations[annotationGroupDBNamespace]) != "" ||
				strings.TrimSpace(pg.Annotations[annotationGroupDBProvider]) != "")
		if hasPinned {
			children, cErr := r.listChildEnvironments(ctx, pg)
			if cErr != nil {
				return nil, "", true, false, false, cErr
			}
			for i := range children {
				secName := sanitizeK8sName(fmt.Sprintf("diverge-db-env-%s", children[i].Name), children[i].Name, 63)
				inUse, useErr := r.childOrWorkloadStillUsesSharedDBSecret(ctx, &children[i], secName)
				if useErr != nil {
					return nil, "", true, false, false, useErr
				}
				if inUse {
					// Wait until live child Environments are updated AND their workloads/Pods have released the Secret before tearing down shared DB / Secrets.
					return nil, "", false, true, true, nil
				}
			}
			if err := r.teardownGroupDatabase(ctx, pg); err != nil {
				return nil, "", true, false, false, err
			}
			baseCopy := pg.DeepCopy()
			delete(pg.Annotations, annotationGroupDBNamespace)
			delete(pg.Annotations, annotationGroupDBProvider)
			delete(pg.Annotations, annotationGroupDBMigrationHash)
			if err := r.Patch(ctx, pg, client.MergeFrom(baseCopy)); err != nil {
				return nil, "", true, false, false, fmt.Errorf("failed to clear group database annotations: %w", err)
			}
			return nil, "", false, true, false, nil
		}
		return nil, "", false, true, false, nil
	}

	anchorNS = resolveGroupDatabaseAnchorNamespace(pg)
	anchorEnv := r.buildGroupDatabaseAnchor(pg, anchorNS)
	helper := r.envProvisionHelper()

	target, err := helper.resolveTarget(ctx, anchorEnv)
	if err != nil {
		return nil, anchorNS, true, false, false, err
	}
	tCtx := ctx
	if target != nil {
		tCtx = multicluster.WithTarget(ctx, target)
	}

	provCtx, cancel := context.WithTimeout(tCtx, 20*time.Second)
	defer cancel()

	dbProv, resolvedName, err := helper.resolveDatabaseProviderWithContext(provCtx, anchorEnv, false)
	if err != nil {
		return nil, anchorNS, true, false, false, err
	}
	if dbProv != nil && dbProv != r.DatabaseProvider {
		defer closeDatabaseProvider(dbProv)
	}
	if dbProv == nil || isDefaultOrNoopDBProvider(dbProv) {
		// No concrete database provider configured; leave child spec intact for test/noop setups.
		return nil, anchorNS, false, true, false, nil
	}

	// Write-ahead persist the group DB anchor namespace and provider before external provisioning so teardown is deterministic.
	needPatch := false
	baseCopy := pg.DeepCopy()
	if pg.Annotations == nil {
		pg.Annotations = make(map[string]string)
	}
	if pg.Annotations[annotationGroupDBNamespace] != anchorNS {
		pg.Annotations[annotationGroupDBNamespace] = anchorNS
		needPatch = true
	}
	if resolvedName != "" && pg.Annotations[annotationGroupDBProvider] == "" {
		pg.Annotations[annotationGroupDBProvider] = resolvedName
		needPatch = true
	}
	if needPatch {
		if patchErr := r.Patch(ctx, pg, client.MergeFrom(baseCopy)); patchErr != nil {
			return nil, anchorNS, true, false, false, fmt.Errorf("failed to persist group database metadata: %w", patchErr)
		}
	}

	dbRes, err := dbProv.Provision(provCtx, anchorEnv)
	if err != nil {
		return nil, anchorNS, true, false, false, err
	}
	if dbRes == nil || !dbRes.Ready {
		return nil, anchorNS, true, false, false, nil
	}

	if dbRes.SetupSQL != "" && !dbRes.SetupSQLExecuted {
		runner := r.SetupRunner
		if runner == nil {
			runner = &DirectSetupRunner{}
		}
		if setupErr := runner.RunSetupSQL(tCtx, anchorEnv, dbRes.SetupSQL, dbRes.AdminDSN); setupErr != nil {
			if errors.Is(setupErr, ErrHookInProgress) {
				return nil, anchorNS, true, false, false, nil
			}
			return nil, anchorNS, true, false, false, setupErr
		}
	}

	// Execute group-level MigrationJob / Atlas ONCE per digest against the shared database (recording completion only after success).
	migInProgressRequeue := false
	if pg.Spec.Database != nil && (pg.Spec.Database.MigrationJob != nil || pg.Spec.Database.Atlas != nil) {
		migDigest := groupDatabaseMigrationDigest(pg, anchorEnv.Name, anchorNS)
		if pg.Annotations[annotationGroupDBMigrationHash] != migDigest {
			migHelper := helper
			if target != nil && target.IsRemote && target.Client != nil {
				migHelper = &EnvironmentReconciler{
					Client:           target.Client,
					Scheme:           r.Scheme,
					DatabaseProvider: r.DatabaseProvider,
					ClusterClients:   r.ClusterClients,
					SetupRunner:      r.SetupRunner,
				}
			}
			obsAnchor := blockingObservingAnchor(anchorEnv)
			if migErr := migHelper.runMigrations(tCtx, obsAnchor, dbRes); migErr != nil {
				if errors.Is(migErr, ErrHookInProgress) {
					if isGroupMigrationBlocking(pg.Spec.Database) {
						return nil, anchorNS, true, false, false, nil
					}
					migInProgressRequeue = true
				} else if errors.Is(migErr, ErrHookFailed) && !isGroupMigrationBlocking(pg.Spec.Database) {
					log.FromContext(ctx).Error(migErr, "non-blocking shared group database migration failed")
					if r.Recorder != nil {
						r.Recorder.Eventf(pg, "Warning", "GroupMigrationFailed", "Non-blocking shared group database migration failed: %v", migErr)
					}
				} else {
					return nil, anchorNS, true, false, false, migErr
				}
			} else {
				migBase := pg.DeepCopy()
				if pg.Annotations == nil {
					pg.Annotations = make(map[string]string)
				}
				pg.Annotations[annotationGroupDBMigrationHash] = migDigest
				if patchErr := r.Patch(ctx, pg, client.MergeFrom(migBase)); patchErr != nil {
					return nil, anchorNS, true, false, false, fmt.Errorf("failed to persist group migration completion: %w", patchErr)
				}
			}
		}
	}

	outVars := make(map[string]string, len(dbRes.EnvVars)+1)
	for k, v := range dbRes.EnvVars {
		outVars[k] = v
	}
	if len(outVars) == 0 && strings.TrimSpace(dbRes.DSN) != "" {
		outVars["DATABASE_URL"] = dbRes.DSN
	}
	if len(outVars) == 0 {
		return nil, anchorNS, true, false, false, nil
	}
	return outVars, anchorNS, true, true, migInProgressRequeue, nil
}

func isClusterLocalDBVars(vars map[string]string, anchorEnvName string) bool {
	clusterSvc := cnpg.ClusterName(anchorEnvName) + "-rw"
	for _, v := range vars {
		if strings.Contains(v, clusterSvc) || strings.Contains(v, ".svc") {
			return true
		}
	}
	return false
}

func rewriteGroupDBVarsForNamespace(baseVars map[string]string, anchorEnvName, anchorNS, childNS string) map[string]string {
	out := make(map[string]string, len(baseVars))
	clusterSvc := cnpg.ClusterName(anchorEnvName) + "-rw"
	// Use <svc>.<namespace>.svc so cross-namespace pods resolve via their cluster DNS search path
	// regardless of custom Kubernetes cluster domain (cluster.local, corp.internal, etc.).
	qualifiedSvc := fmt.Sprintf("%s.%s.svc", clusterSvc, anchorNS)
	crossNS := anchorNS != "" && childNS != "" && anchorNS != childNS

	for k, v := range baseVars {
		if crossNS {
			if parsed, err := url.Parse(v); err == nil && parsed.Host != "" {
				host := parsed.Hostname()
				if host == clusterSvc {
					if port := parsed.Port(); port != "" {
						parsed.Host = net.JoinHostPort(qualifiedSvc, port)
					} else {
						parsed.Host = qualifiedSvc
					}
					v = parsed.String()
				}
			} else {
				if strings.Contains(v, "@"+clusterSvc+":") {
					v = strings.ReplaceAll(v, "@"+clusterSvc+":", "@"+qualifiedSvc+":")
				}
				if strings.Contains(v, "@"+clusterSvc+"/") {
					v = strings.ReplaceAll(v, "@"+clusterSvc+"/", "@"+qualifiedSvc+"/")
				}
			}
		}
		out[k] = v
	}
	return out
}

func (r *PreviewGroupReconciler) ensureChildSharedDBSecret(
	ctx context.Context,
	pg *divergeiov1alpha1.PreviewGroup,
	svc divergeiov1alpha1.PreviewGroupServiceSpec,
	childEnvName, targetNS, anchorNS string,
	groupVars map[string]string,
) (string, error) {
	if len(groupVars) == 0 {
		return "", nil
	}
	helper := r.envProvisionHelper()
	var effectiveClusterRef *divergeiov1alpha1.ClusterTargetRef
	if pg.Spec.ClusterRef != nil {
		effectiveClusterRef = pg.Spec.ClusterRef.DeepCopy()
	}
	if svc.ClusterRef != nil {
		effectiveClusterRef = svc.ClusterRef.DeepCopy()
	}
	if isClusterLocalDBVars(groupVars, groupDatabaseAnchorName(pg.Name)) &&
		!divergeiov1alpha1.EqualClusterTargetRefInNamespaces(pg.Spec.ClusterRef, anchorNS, effectiveClusterRef, targetNS) {
		return "", fmt.Errorf("service %q targets a different cluster than shared group database in %q; cluster-local databases cannot be routed across clusters", svc.Name, anchorNS)
	}
	probeEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: childEnvName, Namespace: targetNS},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace:  "same",
				ClusterRef: effectiveClusterRef,
			},
		},
	}
	targetClient, _, _, err := helper.resolveTargetCluster(ctx, probeEnv)
	if err != nil {
		return "", err
	}

	secretName := sanitizeK8sName(fmt.Sprintf("diverge-db-env-%s", childEnvName), childEnvName, 63)
	varsForChild := rewriteGroupDBVarsForNamespace(groupVars, groupDatabaseAnchorName(pg.Name), anchorNS, targetNS)

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: targetNS,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, targetClient, sec, func() error {
		if sec.Labels == nil {
			sec.Labels = make(map[string]string)
		}
		sec.Labels["divergedev.com/environment"] = truncateLabel(childEnvName)
		sec.Labels[labelPreviewGroup] = truncateLabel(pg.Name)
		sec.Labels["divergedev.com/managed-by"] = "diverge"
		data := make(map[string][]byte, len(varsForChild))
		for k, v := range varsForChild {
			data[k] = []byte(v)
		}
		sec.Data = data
		sec.StringData = nil
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to ensure shared database secret %s/%s: %w", targetNS, secretName, err)
	}
	return secretName, nil
}

func (r *PreviewGroupReconciler) teardownGroupDatabase(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) error {
	hasPinned := pg != nil && pg.Annotations != nil &&
		(strings.TrimSpace(pg.Annotations[annotationGroupDBNamespace]) != "" ||
			strings.TrimSpace(pg.Annotations[annotationGroupDBProvider]) != "")
	if !hasDedicatedGroupDatabase(pg) && !hasPinned {
		return nil
	}

	anchorNS := resolveGroupDatabaseAnchorNamespace(pg)
	anchorEnv := r.buildGroupDatabaseAnchor(pg, anchorNS)
	helper := r.envProvisionHelper()

	baseTDCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tdCtx := baseTDCtx

	targetClient := r.Client
	target, tErr := helper.resolveTarget(tdCtx, anchorEnv)
	if tErr != nil {
		if multicluster.IsRemoteEnv(anchorEnv) {
			return fmt.Errorf("failed to resolve database target cluster for teardown: %w", tErr)
		}
	} else if target != nil {
		tdCtx = multicluster.WithTarget(baseTDCtx, target)
		if target.Client != nil {
			targetClient = target.Client
		}
	}

	dbProv, _, err := helper.resolveDatabaseProviderWithContext(tdCtx, anchorEnv, true)
	if err != nil {
		return err
	}
	if dbProv != nil && dbProv != r.DatabaseProvider {
		defer closeDatabaseProvider(dbProv)
	}
	if dbProv != nil && !isDefaultOrNoopDBProvider(dbProv) {
		if err := dbProv.Teardown(tdCtx, anchorEnv); err != nil {
			return err
		}
	}

	// Clean up any anchor migration/setup-sql DSN Secrets, Atlas CRs, and per-child shared DB Secrets
	var errs []error
	for _, labelVal := range []string{truncateLabel(anchorEnv.Name), truncateLabel(anchorEnv.Name + "-admin")} {
		var secList corev1.SecretList
		if lErr := targetClient.List(tdCtx, &secList, client.InNamespace(anchorNS), client.MatchingLabels{
			labelEnvironment: labelVal,
		}); lErr != nil {
			errs = append(errs, fmt.Errorf("failed to list migration/setup secrets in %s: %w", anchorNS, lErr))
			continue
		}
		for i := range secList.Items {
			hk := secList.Items[i].Labels[labelHookType]
			if hk == hookTypeMigration || hk == hookTypeSetupSQL {
				if dErr := targetClient.Delete(tdCtx, &secList.Items[i]); dErr != nil && !apierrors.IsNotFound(dErr) {
					errs = append(errs, fmt.Errorf("failed to delete %s secret %s/%s: %w", hk, anchorNS, secList.Items[i].Name, dErr))
				}
			}
		}
	}
	if anchorEnv.Spec.Database.Atlas != nil && anchorEnv.Spec.Database.Atlas.Engine != "job" {
		kind := "AtlasMigration"
		if anchorEnv.Spec.Database.Atlas.Mode == "declarative" {
			kind = "AtlasSchema"
		}
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("db.atlasgo.io/v1alpha1")
		u.SetKind(kind)
		u.SetName(generateHookJobName(anchorEnv.Name, "atlas"))
		u.SetNamespace(anchorNS)
		if dErr := targetClient.Delete(tdCtx, u); dErr != nil && !apierrors.IsNotFound(dErr) && !meta.IsNoMatchError(dErr) {
			errs = append(errs, fmt.Errorf("failed to delete %s %s/%s: %w", kind, anchorNS, u.GetName(), dErr))
		}
	}
	if _, secErr := r.cleanupOrphanedChildSharedDBSecrets(baseTDCtx, pg, nil); secErr != nil {
		errs = append(errs, secErr)
	}
	return errors.Join(errs...)
}

func (r *PreviewGroupReconciler) cleanupOrphanedChildSharedDBSecrets(
	ctx context.Context,
	pg *divergeiov1alpha1.PreviewGroup,
	desiredEnvNames map[string]bool,
) (bool, error) {
	if pg == nil || r.Client == nil {
		return false, nil
	}
	children, err := r.listChildEnvironments(ctx, pg)
	if err != nil {
		return false, fmt.Errorf("failed to list child Environments for shared DB Secret cleanup: %w", err)
	}
	sharedDesiredKeys := make(map[string]bool)
	if hasDedicatedGroupDatabase(pg) {
		for _, svc := range pg.Spec.Services {
			if svc.Mode == divergeiov1alpha1.ServiceModeBaseline || svc.Database != nil {
				continue
			}
			svcNS := strings.TrimSpace(svc.Namespace)
			if svcNS == "" {
				svcNS = "default"
			}
			sharedDesiredKeys[svcNS+"/"+childEnvironmentName(pg.Name, svc.Name)] = true
		}
	}
	keepSecrets := make(map[string]bool, len(children)+len(desiredEnvNames))
	deferredKeepSecrets := make(map[string]bool)
	var errs []error
	for i := range children {
		secName := sanitizeK8sName(fmt.Sprintf("diverge-db-env-%s", children[i].Name), children[i].Name, 63)
		secKey := children[i].Namespace + "/" + secName
		inUse, useErr := r.childOrWorkloadStillUsesSharedDBSecret(ctx, &children[i], secName)
		if useErr != nil {
			keepSecrets[secKey] = true
			errs = append(errs, useErr)
			continue
		}
		if inUse {
			keepSecrets[secKey] = true
			if !sharedDesiredKeys[children[i].Namespace+"/"+children[i].Name] {
				deferredKeepSecrets[secKey] = true
			}
		}
	}
	for envKey := range desiredEnvNames {
		if !sharedDesiredKeys[envKey] {
			continue
		}
		parts := strings.SplitN(envKey, "/", 2)
		if len(parts) != 2 {
			continue
		}
		secName := sanitizeK8sName(fmt.Sprintf("diverge-db-env-%s", parts[1]), parts[1], 63)
		keepSecrets[parts[0]+"/"+secName] = true
	}

	type targetScan struct {
		client     client.Client
		namespaces []string
		isRemote   bool
	}
	scans := []targetScan{{client: r.Client}}
	seenTargets := map[string]int{}
	remoteScanKey := func(target *multicluster.Target, ns string, ref *divergeiov1alpha1.ClusterTargetRef) string {
		if target.Client != nil {
			v := reflect.ValueOf(target.Client)
			switch v.Kind() {
			case reflect.Pointer, reflect.Map, reflect.Chan, reflect.Func, reflect.UnsafePointer:
				if p := v.Pointer(); p != 0 {
					return fmt.Sprintf("ptr:%x", p)
				}
			}
		}
		if ref != nil && ref.KubeconfigSecretRef != nil {
			secNS := strings.TrimSpace(ref.KubeconfigSecretRef.Namespace)
			if secNS == "" {
				secNS = strings.TrimSpace(ns)
			}
			secName := strings.TrimSpace(ref.KubeconfigSecretRef.Name)
			secKey := strings.TrimSpace(ref.KubeconfigSecretRef.Key)
			if secKey == "" {
				secKey = "kubeconfig"
			}
			if secName != "" {
				return fmt.Sprintf("secret:%s/%s/%s", secNS, secName, secKey)
			}
		}
		return "name:" + target.Name
	}
	addRemoteScan := func(target *multicluster.Target, ns string, ref *divergeiov1alpha1.ClusterTargetRef) {
		if target == nil || !target.IsRemote || target.Client == nil {
			return
		}
		key := remoteScanKey(target, ns, ref)
		if idx, ok := seenTargets[key]; ok {
			if ns != "" && !containsString(scans[idx].namespaces, ns) {
				scans[idx].namespaces = append(scans[idx].namespaces, ns)
			}
			return
		}
		var nss []string
		if ns != "" {
			nss = []string{ns}
		}
		seenTargets[key] = len(scans)
		scans = append(scans, targetScan{client: target.Client, namespaces: nss, isRemote: true})
	}

	helper := r.envProvisionHelper()
	anchorNS := resolveGroupDatabaseAnchorNamespace(pg)
	if pg.Spec.ClusterRef != nil {
		probeAnchor := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: groupDatabaseAnchorName(pg.Name), Namespace: anchorNS},
			Spec:       divergeiov1alpha1.EnvironmentSpec{Deploy: divergeiov1alpha1.EnvironmentDeploy{Namespace: "same", ClusterRef: pg.Spec.ClusterRef}},
		}
		if t, tErr := helper.resolveTarget(ctx, probeAnchor); tErr != nil {
			if multicluster.IsRemoteEnv(probeAnchor) {
				errs = append(errs, fmt.Errorf("failed to resolve anchor spoke cluster for shared DB Secret cleanup: %w", tErr))
			}
		} else {
			addRemoteScan(t, anchorNS, pg.Spec.ClusterRef)
		}
	}
	for _, svc := range pg.Spec.Services {
		svcNS := strings.TrimSpace(svc.Namespace)
		if svcNS == "" {
			svcNS = "default"
		}
		effClusterRef := pg.Spec.ClusterRef
		if svc.ClusterRef != nil {
			effClusterRef = svc.ClusterRef
		}
		if effClusterRef != nil {
			probeSvc := &divergeiov1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: childEnvironmentName(pg.Name, svc.Name), Namespace: svcNS},
				Spec:       divergeiov1alpha1.EnvironmentSpec{Deploy: divergeiov1alpha1.EnvironmentDeploy{Namespace: "same", ClusterRef: effClusterRef}},
			}
			if t, tErr := helper.resolveTarget(ctx, probeSvc); tErr != nil {
				if multicluster.IsRemoteEnv(probeSvc) {
					errs = append(errs, fmt.Errorf("failed to resolve spoke cluster for service %q shared DB Secret cleanup: %w", svc.Name, tErr))
				}
			} else {
				addRemoteScan(t, svcNS, effClusterRef)
			}
		}
	}
	for i := range children {
		child := &children[i]
		if child.Spec.Deploy.ClusterRef == nil {
			continue
		}
		probeChild := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: child.Name, Namespace: child.Namespace},
			Spec:       divergeiov1alpha1.EnvironmentSpec{Deploy: divergeiov1alpha1.EnvironmentDeploy{Namespace: "same", ClusterRef: child.Spec.Deploy.ClusterRef}},
		}
		if t, tErr := helper.resolveTarget(ctx, probeChild); tErr != nil {
			if multicluster.IsRemoteEnv(probeChild) {
				errs = append(errs, fmt.Errorf("failed to resolve spoke cluster for child %s/%s shared DB Secret cleanup: %w", child.Namespace, child.Name, tErr))
			}
		} else {
			addRemoteScan(t, child.Namespace, child.Spec.Deploy.ClusterRef)
		}
	}

	matchLabels := client.MatchingLabels{
		labelPreviewGroup:           truncateLabel(pg.Name),
		"divergedev.com/managed-by": "diverge",
	}
	requeueNeeded := false
	for _, scan := range scans {
		var secList corev1.SecretList
		if lErr := scan.client.List(ctx, &secList, matchLabels); lErr != nil {
			if meta.IsNoMatchError(lErr) || runtime.IsNotRegisteredError(lErr) {
				continue
			}
			if scan.isRemote && apierrors.IsForbidden(lErr) && len(scan.namespaces) > 0 {
				for _, ns := range scan.namespaces {
					var nsList corev1.SecretList
					if nsErr := scan.client.List(ctx, &nsList, client.InNamespace(ns), matchLabels); nsErr != nil {
						if meta.IsNoMatchError(nsErr) || runtime.IsNotRegisteredError(nsErr) {
							continue
						}
						errs = append(errs, fmt.Errorf("failed to list shared DB Secrets in %s: %w", ns, nsErr))
						continue
					}
					secList.Items = append(secList.Items, nsList.Items...)
				}
			} else {
				errs = append(errs, fmt.Errorf("failed to list shared DB Secrets: %w", lErr))
				continue
			}
		}
		for i := range secList.Items {
			sec := &secList.Items[i]
			if !strings.HasPrefix(sec.Name, "diverge-db-env-") {
				continue
			}
			secKey := sec.Namespace + "/" + sec.Name
			if keepSecrets[secKey] {
				if deferredKeepSecrets[secKey] {
					requeueNeeded = true
				}
				continue
			}
			if dErr := scan.client.Delete(ctx, sec); dErr != nil && !apierrors.IsNotFound(dErr) {
				errs = append(errs, fmt.Errorf("failed to delete orphaned shared DB Secret %s/%s: %w", sec.Namespace, sec.Name, dErr))
			}
		}
	}
	return requeueNeeded, errors.Join(errs...)
}

// syncMemberStandaloneEnvironments hibernates standalone PR Environments whose replacement child service has reached
// Running (within the same authorized service namespace) and wakes any detached or failed-replacement Environments.
func (r *PreviewGroupReconciler) syncMemberStandaloneEnvironments(
	ctx context.Context,
	pg *divergeiov1alpha1.PreviewGroup,
	servicePhases map[string]divergeiov1alpha1.EnvironmentPhase,
) error {
	if pg == nil {
		return nil
	}
	activeServiceNS := make(map[string]string, len(pg.Spec.Services))
	for _, svc := range pg.Spec.Services {
		if svc.Mode == divergeiov1alpha1.ServiceModeBaseline {
			continue
		}
		ns := strings.TrimSpace(svc.Namespace)
		if ns == "" {
			ns = "default"
		}
		activeServiceNS[svc.Name] = ns
	}

	claimed := make(map[string]bool)
	nowRFC3339 := time.Now().UTC().Format(time.RFC3339)
	for _, m := range previewgroup.ParseMembers(pg) {
		envName := strings.TrimSpace(m.EnvironmentName)
		if envName == "" {
			continue
		}
		svcNS, ok := activeServiceNS[m.Service]
		if !ok {
			continue
		}
		envNS := strings.TrimSpace(m.EnvironmentNamespace)
		if envNS == "" {
			envNS = strings.TrimSpace(m.Namespace)
		}
		if envNS == "" {
			envNS = svcNS
		}
		// Security invariant: never hibernate an Environment outside the service's own namespace.
		if envNS != svcNS {
			continue
		}
		phase := servicePhases[m.Service]
		if phase == divergeiov1alpha1.PhaseFailed {
			// Do not claim or hibernate standalone preview when its replacement service failed; wake it if needed.
			continue
		}

		var standalone divergeiov1alpha1.Environment
		if err := r.Get(ctx, types.NamespacedName{Namespace: envNS, Name: envName}, &standalone); err != nil {
			continue
		}
		if !standalone.DeletionTimestamp.IsZero() || standalone.Labels[labelManagedBy] == "diverge-previewgroup" {
			continue
		}
		// Security invariant: bind each member record to an Environment whose Spec.Source matches the member's SCM identity.
		if !previewgroup.MemberMatchesEnvironmentSource(m, &standalone) {
			continue
		}
		claimed[envNS+"/"+envName] = true
		if phase != divergeiov1alpha1.PhaseRunning {
			// Keep standalone preview running until its group replacement reaches Running.
			continue
		}
		if standalone.Annotations[metrics.AnnotationHibernated] == "true" &&
			standalone.Annotations[previewgroup.AnnotationAttachedPreviewGroup] == pg.Name {
			continue
		}
		base := standalone.DeepCopy()
		if standalone.Annotations == nil {
			standalone.Annotations = make(map[string]string)
		}
		if standalone.Annotations[metrics.AnnotationHibernated] != "true" {
			standalone.Annotations[metrics.AnnotationHibernated] = "true"
			if standalone.Annotations[metrics.AnnotationHibernatedAt] == "" {
				standalone.Annotations[metrics.AnnotationHibernatedAt] = nowRFC3339
			}
			standalone.Annotations[previewgroup.AnnotationAttachedPreviewGroup] = pg.Name
			standalone.Annotations[previewgroup.AnnotationGroupHibernated] = "true"
			_ = r.Patch(ctx, &standalone, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
		} else if strings.TrimSpace(standalone.Annotations[previewgroup.AnnotationAttachedPreviewGroup]) == "" {
			// Preserve independent manual/idle hibernation ("false") so detaching later does not resume a paused preview.
			standalone.Annotations[previewgroup.AnnotationAttachedPreviewGroup] = pg.Name
			standalone.Annotations[previewgroup.AnnotationGroupHibernated] = "false"
			_ = r.Patch(ctx, &standalone, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
		}
	}

	return r.wakeUnclaimedStandaloneEnvironments(ctx, pg.Name, claimed)
}

// wakeUnclaimedStandaloneEnvironments wakes standalone Environments whose attached-previewgroup annotation
// points to groupName when they are no longer claimed by groupName (or re-points them if another active group claims them).
func (r *PreviewGroupReconciler) wakeUnclaimedStandaloneEnvironments(ctx context.Context, groupName string, stillClaimed map[string]bool) error {
	if strings.TrimSpace(groupName) == "" {
		return nil
	}
	var envList divergeiov1alpha1.EnvironmentList
	if err := r.List(ctx, &envList); err != nil {
		return fmt.Errorf("failed to list Environments for wake check: %w", err)
	}

	var candidates []*divergeiov1alpha1.Environment
	childEnvByKey := make(map[string]*divergeiov1alpha1.Environment)
	for i := range envList.Items {
		env := &envList.Items[i]
		if !env.DeletionTimestamp.IsZero() {
			continue
		}
		if env.Labels[labelManagedBy] == "diverge-previewgroup" {
			childEnvByKey[env.Namespace+"/"+env.Name] = env
			continue
		}
		if env.Annotations[previewgroup.AnnotationAttachedPreviewGroup] != groupName {
			continue
		}
		if stillClaimed != nil && stillClaimed[env.Namespace+"/"+env.Name] {
			continue
		}
		candidates = append(candidates, env)
	}
	if len(candidates) == 0 {
		return nil
	}

	// Check whether any other active PreviewGroup still claims each candidate Environment.
	// Propagate List errors rather than assuming no other group exists.
	var pgList divergeiov1alpha1.PreviewGroupList
	if err := r.List(ctx, &pgList); err != nil {
		return fmt.Errorf("failed to list PreviewGroups before waking standalone Environments: %w", err)
	}
	candidateByKey := make(map[string]*divergeiov1alpha1.Environment, len(candidates))
	for _, c := range candidates {
		candidateByKey[c.Namespace+"/"+c.Name] = c
	}

	otherOwner := make(map[string]string)
	for i := range pgList.Items {
		other := &pgList.Items[i]
		if other.Name == groupName || !other.DeletionTimestamp.IsZero() {
			continue
		}
		if other.Generation > 0 {
			readyCond := meta.FindStatusCondition(other.Status.Conditions, "Ready")
			if readyCond == nil || readyCond.ObservedGeneration < other.Generation {
				continue
			}
		}
		otherStatusPhase := make(map[string]divergeiov1alpha1.EnvironmentPhase, len(other.Status.Services))
		for _, st := range other.Status.Services {
			otherStatusPhase[st.Name] = st.Phase
		}
		svcNS := make(map[string]string, len(other.Spec.Services))
		svcSpecByName := make(map[string]divergeiov1alpha1.PreviewGroupServiceSpec, len(other.Spec.Services))
		for _, s := range other.Spec.Services {
			if s.Mode == divergeiov1alpha1.ServiceModeBaseline {
				continue
			}
			ns := strings.TrimSpace(s.Namespace)
			if ns == "" {
				ns = "default"
			}
			svcNS[s.Name] = ns
			svcSpecByName[s.Name] = s
		}
		for _, m := range previewgroup.ParseMembers(other) {
			if m.EnvironmentName == "" {
				continue
			}
			ns, ok := svcNS[m.Service]
			if !ok {
				continue
			}
			envNS := strings.TrimSpace(m.EnvironmentNamespace)
			if envNS == "" {
				envNS = strings.TrimSpace(m.Namespace)
			}
			if envNS == "" {
				envNS = ns
			}
			if envNS != ns {
				continue
			}
			if otherStatusPhase[m.Service] != "" && otherStatusPhase[m.Service] != divergeiov1alpha1.PhaseRunning {
				continue
			}
			child := childEnvByKey[ns+"/"+childEnvironmentName(other.Name, m.Service)]
			if child == nil || child.Status.Phase != divergeiov1alpha1.PhaseRunning ||
				(child.Generation > 0 && child.Status.ObservedGeneration < child.Generation) ||
				meta.IsStatusConditionFalse(child.Status.Conditions, "DatabaseReady") {
				continue
			}
			svcSpec := svcSpecByName[m.Service]
			if svcSpec.Image != "" && (child.Spec.ServiceConfig == nil || child.Spec.ServiceConfig.Image != svcSpec.Image) {
				continue
			}
			if cand, exists := candidateByKey[envNS+"/"+m.EnvironmentName]; exists && previewgroup.MemberMatchesEnvironmentSource(m, cand) {
				otherOwner[envNS+"/"+m.EnvironmentName] = other.Name
			}
		}
	}

	var errs []error
	for _, env := range candidates {
		base := env.DeepCopy()
		if env.Annotations == nil {
			continue
		}
		if replacementGroup := otherOwner[env.Namespace+"/"+env.Name]; replacementGroup != "" {
			env.Annotations[previewgroup.AnnotationAttachedPreviewGroup] = replacementGroup
		} else {
			if env.Annotations[previewgroup.AnnotationGroupHibernated] != "false" {
				delete(env.Annotations, metrics.AnnotationHibernated)
			}
			delete(env.Annotations, previewgroup.AnnotationAttachedPreviewGroup)
			delete(env.Annotations, previewgroup.AnnotationGroupHibernated)
		}
		if err := r.Patch(ctx, env, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to wake standalone Environment %s/%s: %w", env.Namespace, env.Name, err))
		}
	}
	return errors.Join(errs...)
}
