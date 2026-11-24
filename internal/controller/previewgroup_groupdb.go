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
	"github.com/divergedev/diverge/internal/database"
	"github.com/divergedev/diverge/internal/database/cnpg"
	"github.com/divergedev/diverge/internal/metrics"
	"github.com/divergedev/diverge/internal/multicluster"
	"github.com/divergedev/diverge/internal/previewgroup"
)

const (
	annotationGroupDBNamespace      = "divergedev.com/group-db-namespace"
	annotationGroupDBProvider       = "divergedev.com/group-db-provider"
	annotationGroupDBMigrationHash  = "divergedev.com/group-db-migration-hash"
	annotationGroupDBSecretTargets  = "divergedev.com/group-db-secret-targets"
	labelSharedGroupDBSecret        = "divergedev.com/shared-group-db-secret"
	annotationSharedGroupDBDraining = "divergedev.com/shared-group-db-draining"
	annotationLegacyGroupDBDraining = "divergedev.com/legacy-group-db-draining"
	annotationLegacyGroupDBOwner    = "divergedev.com/legacy-group-db-owner"
	legacyGroupDBDrainingPending    = "pending"
	legacyGroupDBDrainingUIDPrefix  = "uids:"
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

func sharedGroupDBSecretName(childEnvName string) string {
	return database.GroupEnvSecretName(childEnvName)
}

func legacySharedGroupDBSecretName(childEnvName string) string {
	return database.EnvSecretName(childEnvName)
}

func podTrackID(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	if pod.UID != "" {
		return string(pod.UID)
	}
	return "name:" + pod.Name
}

type recordedGroupDBSecretTarget struct {
	Namespace  string                              `json:"namespace"`
	ClusterRef *divergeiov1alpha1.ClusterTargetRef `json:"clusterRef,omitempty"`
}

func parseRecordedSharedDBSecretTargets(pg *divergeiov1alpha1.PreviewGroup) []recordedGroupDBSecretTarget {
	if pg == nil || pg.Annotations == nil {
		return nil
	}
	raw := strings.TrimSpace(pg.Annotations[annotationGroupDBSecretTargets])
	if raw == "" {
		return nil
	}
	var out []recordedGroupDBSecretTarget
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func hasRecordedSharedDBSecretTargets(pg *divergeiov1alpha1.PreviewGroup) bool {
	return len(parseRecordedSharedDBSecretTargets(pg)) > 0
}

func canonicalClusterRefKey(ns string, ref *divergeiov1alpha1.ClusterTargetRef) string {
	normNS := strings.TrimSpace(ns)
	if normNS == "" {
		normNS = "default"
	}
	if ref == nil {
		return normNS + "|local"
	}
	name := strings.TrimSpace(ref.Name)
	var secNS, secName, secKey string
	if ref.KubeconfigSecretRef != nil {
		secNS = strings.TrimSpace(ref.KubeconfigSecretRef.Namespace)
		if secNS == "" {
			secNS = normNS
		}
		secName = strings.TrimSpace(ref.KubeconfigSecretRef.Name)
		secKey = strings.TrimSpace(ref.KubeconfigSecretRef.Key)
		if secKey == "" {
			secKey = "kubeconfig"
		}
	}
	if name == "" {
		name = secName
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s", normNS, name, secNS, secName, secKey)
}

func (r *PreviewGroupReconciler) recordSharedDBSecretTarget(
	ctx context.Context,
	pg *divergeiov1alpha1.PreviewGroup,
	targetNS string,
	ref *divergeiov1alpha1.ClusterTargetRef,
) error {
	if r == nil || r.Client == nil || pg == nil || ref == nil {
		return nil
	}
	normNS := strings.TrimSpace(targetNS)
	if normNS == "" {
		normNS = "default"
	}
	key := canonicalClusterRefKey(normNS, ref)
	existing := parseRecordedSharedDBSecretTargets(pg)
	for _, rt := range existing {
		if canonicalClusterRefKey(rt.Namespace, rt.ClusterRef) == key {
			return nil
		}
	}
	updated := append(existing, recordedGroupDBSecretTarget{
		Namespace:  normNS,
		ClusterRef: ref.DeepCopy(),
	})
	encoded, err := json.Marshal(updated)
	if err != nil {
		return fmt.Errorf("failed to marshal recorded shared DB Secret targets: %w", err)
	}
	desiredStatus := pg.Status.DeepCopy()
	base := pg.DeepCopy()
	if pg.Annotations == nil {
		pg.Annotations = make(map[string]string)
	}
	pg.Annotations[annotationGroupDBSecretTargets] = string(encoded)
	if err := r.Patch(ctx, pg, client.MergeFrom(base)); err != nil {
		pg.Status = *desiredStatus
		return fmt.Errorf("failed to persist recorded shared DB Secret target %s: %w", normNS, err)
	}
	pg.Status = *desiredStatus
	return nil
}

func (r *PreviewGroupReconciler) pruneRecordedSharedDBSecretTargets(
	ctx context.Context,
	pg *divergeiov1alpha1.PreviewGroup,
	removableKeys map[string]bool,
) error {
	if r == nil || r.Client == nil || pg == nil || len(removableKeys) == 0 {
		return nil
	}
	existing := parseRecordedSharedDBSecretTargets(pg)
	if len(existing) == 0 {
		return nil
	}
	var kept []recordedGroupDBSecretTarget
	changed := false
	for _, rt := range existing {
		if removableKeys[canonicalClusterRefKey(rt.Namespace, rt.ClusterRef)] {
			changed = true
			continue
		}
		kept = append(kept, rt)
	}
	if !changed {
		return nil
	}
	desiredStatus := pg.Status.DeepCopy()
	base := pg.DeepCopy()
	if len(kept) == 0 {
		delete(pg.Annotations, annotationGroupDBSecretTargets)
	} else {
		encoded, err := json.Marshal(kept)
		if err != nil {
			return fmt.Errorf("failed to marshal remaining shared DB Secret targets: %w", err)
		}
		if pg.Annotations == nil {
			pg.Annotations = make(map[string]string)
		}
		pg.Annotations[annotationGroupDBSecretTargets] = string(encoded)
	}
	if err := r.Patch(ctx, pg, client.MergeFrom(base)); err != nil {
		pg.Status = *desiredStatus
		return fmt.Errorf("failed to prune recorded shared DB Secret targets: %w", err)
	}
	pg.Status = *desiredStatus
	return nil
}

func hasEnvironmentControllerOwnerRef(sec *corev1.Secret) bool {
	if sec == nil {
		return false
	}
	for i := range sec.OwnerReferences {
		ref := &sec.OwnerReferences[i]
		if ref.Controller != nil && *ref.Controller && ref.Kind == "Environment" {
			return true
		}
	}
	return false
}

func isGroupOwnedSharedDBSecret(sec *corev1.Secret, pgName string) bool {
	if sec == nil {
		return false
	}
	if sec.Labels[labelSharedGroupDBSecret] == "true" || strings.HasPrefix(sec.Name, database.GroupDBEnvSecretPrefix) {
		return true
	}
	return strings.HasPrefix(sec.Name, database.DBEnvSecretPrefix) &&
		pgName != "" &&
		sec.Labels[labelPreviewGroup] == truncateLabel(pgName) &&
		!hasEnvironmentControllerOwnerRef(sec)
}

func childIsReadyAtCurrentGeneration(child *divergeiov1alpha1.Environment) bool {
	if child == nil || child.Generation <= 0 {
		return true
	}
	if child.Status.ObservedGeneration < child.Generation || child.Status.Phase != divergeiov1alpha1.PhaseRunning {
		return false
	}
	servicesReady := meta.FindStatusCondition(child.Status.Conditions, "ServicesReady")
	return servicesReady != nil && servicesReady.Status == metav1.ConditionTrue && servicesReady.ObservedGeneration >= child.Generation
}

func childStillUsesSharedDBSecret(child *divergeiov1alpha1.Environment, sharedSecName string) bool {
	if child == nil {
		return false
	}
	if child.Annotations[annotationSharedGroupDB] == "true" {
		return true
	}
	if sharedSecName != "" && strings.TrimSpace(child.Spec.Database.ConnectionRef) == sharedSecName {
		return true
	}
	if child.Annotations[annotationSharedGroupDBDraining] == "true" {
		return !childIsReadyAtCurrentGeneration(child)
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

func secretNameMatchesAny(candidate string, secNames []string) bool {
	for _, s := range secNames {
		if s != "" && candidate == s {
			return true
		}
	}
	return false
}

func containerRefsAnySecret(envFrom []corev1.EnvFromSource, env []corev1.EnvVar, secNames []string) bool {
	for i := range envFrom {
		if envFrom[i].SecretRef != nil && secretNameMatchesAny(envFrom[i].SecretRef.Name, secNames) {
			return true
		}
	}
	for i := range env {
		if env[i].ValueFrom != nil && env[i].ValueFrom.SecretKeyRef != nil && secretNameMatchesAny(env[i].ValueFrom.SecretKeyRef.Name, secNames) {
			return true
		}
	}
	return false
}

func podSpecReferencesAnySecret(spec *corev1.PodSpec, secNames []string) bool {
	if spec == nil || len(secNames) == 0 {
		return false
	}
	for i := range spec.Containers {
		if containerRefsAnySecret(spec.Containers[i].EnvFrom, spec.Containers[i].Env, secNames) {
			return true
		}
	}
	for i := range spec.InitContainers {
		if containerRefsAnySecret(spec.InitContainers[i].EnvFrom, spec.InitContainers[i].Env, secNames) {
			return true
		}
	}
	for i := range spec.EphemeralContainers {
		if containerRefsAnySecret(spec.EphemeralContainers[i].EnvFrom, spec.EphemeralContainers[i].Env, secNames) {
			return true
		}
	}
	for i := range spec.Volumes {
		vol := &spec.Volumes[i]
		if vol.Secret != nil && secretNameMatchesAny(vol.Secret.SecretName, secNames) {
			return true
		}
		if vol.Projected != nil {
			for j := range vol.Projected.Sources {
				if vol.Projected.Sources[j].Secret != nil && secretNameMatchesAny(vol.Projected.Sources[j].Secret.Name, secNames) {
					return true
				}
			}
		}
	}
	return false
}

func unstructuredContainersRefAnySecret(containers []interface{}, secNames []string) bool {
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
				if sName, _, _ := unstructured.NestedString(efMap, "secretRef", "name"); secretNameMatchesAny(sName, secNames) {
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
				if sName, _, _ := unstructured.NestedString(eMap, "valueFrom", "secretKeyRef", "name"); secretNameMatchesAny(sName, secNames) {
					return true
				}
			}
		}
	}
	return false
}

func unstructuredPodTemplateReferencesAnySecret(obj map[string]interface{}, secNames []string) bool {
	if obj == nil || len(secNames) == 0 {
		return false
	}
	podSpec, found, err := unstructured.NestedMap(obj, "spec", "template", "spec")
	if err != nil || !found || podSpec == nil {
		return false
	}
	for _, key := range []string{"containers", "initContainers", "ephemeralContainers"} {
		if list, ok, _ := unstructured.NestedSlice(podSpec, key); ok && unstructuredContainersRefAnySecret(list, secNames) {
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
		if sName, _, _ := unstructured.NestedString(vMap, "secret", "secretName"); secretNameMatchesAny(sName, secNames) {
			return true
		}
		sources, _, _ := unstructured.NestedSlice(vMap, "projected", "sources")
		for _, srcItem := range sources {
			srcMap, ok := srcItem.(map[string]interface{})
			if !ok {
				continue
			}
			if sName, _, _ := unstructured.NestedString(srcMap, "secret", "name"); secretNameMatchesAny(sName, secNames) {
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

func rolloutStillUsesOrRollingOut(ro *unstructured.Unstructured, childName string, secNames []string, checkRolloutProgress bool) bool {
	if unstructuredPodTemplateReferencesAnySecret(ro.Object, secNames) {
		return true
	}
	if !checkRolloutProgress || !workloadBelongsToChild(ro.GetName(), ro.GetLabels(), childName) {
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

func namespaceWorkloadsStillUseSecret(
	ctx context.Context,
	c client.Client,
	ns, childName string,
	secNames []string,
	checkRolloutProgress bool,
	drainingPodIDs map[string]bool,
	terminatingSecName string,
) (bool, error) {
	if len(secNames) == 0 && len(drainingPodIDs) == 0 && terminatingSecName == "" {
		return false, nil
	}

	var depList appsv1.DeploymentList
	if err := c.List(ctx, &depList, client.InNamespace(ns)); err != nil {
		if !isIgnorableWorkloadListErr(err, false) {
			return true, fmt.Errorf("failed to list deployments in %s for shared DB usage check: %w", ns, err)
		}
	} else {
		for i := range depList.Items {
			dep := &depList.Items[i]
			if podSpecReferencesAnySecret(&dep.Spec.Template.Spec, secNames) {
				return true, nil
			}
			if !checkRolloutProgress || !workloadBelongsToChild(dep.Name, dep.Labels, childName) {
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
			if podSpecReferencesAnySecret(&sts.Spec.Template.Spec, secNames) {
				return true, nil
			}
			if !checkRolloutProgress || !workloadBelongsToChild(sts.Name, sts.Labels, childName) {
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
			if rolloutStillUsesOrRollingOut(&rolloutList.Items[i], childName, secNames, checkRolloutProgress) {
				return true, nil
			}
		}
	}

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
			if len(secNames) > 0 && podSpecReferencesAnySecret(&pod.Spec, secNames) {
				return true, nil
			}
			if len(drainingPodIDs) > 0 && drainingPodIDs[podTrackID(pod)] {
				return true, nil
			}
			if terminatingSecName != "" && !pod.DeletionTimestamp.IsZero() && podSpecReferencesAnySecret(&pod.Spec, []string{terminatingSecName}) {
				return true, nil
			}
		}
	}

	return false, nil
}

func (r *PreviewGroupReconciler) childTargetClientAndNamespaces(
	ctx context.Context,
	child *divergeiov1alpha1.Environment,
) (client.Client, []string, error) {
	targetClient, _, _, err := r.envProvisionHelper().resolveTargetCluster(ctx, child)
	if err != nil {
		if multicluster.IsRemoteEnv(child) {
			return nil, nil, fmt.Errorf("failed to resolve target cluster for child %s/%s shared DB usage check: %w", child.Namespace, child.Name, err)
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
	return targetClient, namespaces, nil
}

func hasGroupOwnedSecretInNamespaces(
	ctx context.Context,
	c client.Client,
	pgName string,
	namespaces []string,
	secretName string,
) (bool, error) {
	if c == nil || secretName == "" {
		return false, nil
	}
	for _, ns := range namespaces {
		var sec corev1.Secret
		if err := c.Get(ctx, types.NamespacedName{Name: secretName, Namespace: ns}, &sec); err != nil {
			if isIgnorableWorkloadListErr(err, true) {
				continue
			}
			return false, fmt.Errorf("failed to check shared DB Secret %s/%s: %w", ns, secretName, err)
		}
		if isGroupOwnedSharedDBSecret(&sec, pgName) {
			return true, nil
		}
	}
	return false, nil
}

func inspectLegacySharedDBSecret(
	ctx context.Context,
	c client.Client,
	pgName string,
	namespaces []string,
	secretName string,
) (hasLegacyGroupSecret bool, takeoverPending bool, drainingPodIDs map[string]bool, drainingSec *corev1.Secret, err error) {
	if c == nil || secretName == "" {
		return false, false, nil, nil, nil
	}
	for _, ns := range namespaces {
		var sec corev1.Secret
		if gErr := c.Get(ctx, types.NamespacedName{Name: secretName, Namespace: ns}, &sec); gErr != nil {
			if isIgnorableWorkloadListErr(gErr, true) {
				continue
			}
			return false, false, nil, nil, fmt.Errorf("failed to check shared DB Secret %s/%s: %w", ns, secretName, gErr)
		}
		if isGroupOwnedSharedDBSecret(&sec, pgName) {
			hasLegacyGroupSecret = true
			continue
		}
		val := strings.TrimSpace(sec.Annotations[annotationLegacyGroupDBDraining])
		if val == "" {
			continue
		}
		owner := strings.TrimSpace(sec.Annotations[annotationLegacyGroupDBOwner])
		if owner != "" && pgName != "" && owner != truncateLabel(pgName) {
			continue
		}
		if val == legacyGroupDBDrainingPending {
			takeoverPending = true
			continue
		}
		if strings.HasPrefix(val, legacyGroupDBDrainingUIDPrefix) {
			rawIDs := strings.TrimPrefix(val, legacyGroupDBDrainingUIDPrefix)
			for _, id := range strings.Split(rawIDs, ",") {
				id = strings.TrimSpace(id)
				if id != "" {
					if drainingPodIDs == nil {
						drainingPodIDs = make(map[string]bool)
					}
					drainingPodIDs[id] = true
				}
			}
			drainingSec = sec.DeepCopy()
		}
	}
	return hasLegacyGroupSecret, takeoverPending, drainingPodIDs, drainingSec, nil
}

// childOrWorkloadStillUsesSharedDBSecret verifies both child Environment status metadata and live
// target-cluster Pods, Deployments, StatefulSets, and Argo Rollouts before allowing shared DB teardown or Secret cleanup.
func (r *PreviewGroupReconciler) childOrWorkloadStillUsesSharedDBSecret(
	ctx context.Context,
	pgName string,
	child *divergeiov1alpha1.Environment,
) (bool, error) {
	if child == nil {
		return false, nil
	}
	sharedSecName := sharedGroupDBSecretName(child.Name)
	legacySecName := legacySharedGroupDBSecretName(child.Name)
	if childStillUsesSharedDBSecret(child, sharedSecName) {
		return true, nil
	}
	if r == nil || r.Client == nil {
		return false, nil
	}

	targetClient, namespaces, err := r.childTargetClientAndNamespaces(ctx, child)
	if err != nil {
		return true, err
	}

	hasGroupSecret, gErr := hasGroupOwnedSecretInNamespaces(ctx, targetClient, pgName, namespaces, sharedSecName)
	if gErr != nil {
		return true, gErr
	}
	hasLegacyGroupSecret, takeoverPending, drainingPodIDs, drainingSec, lErr := inspectLegacySharedDBSecret(ctx, targetClient, pgName, namespaces, legacySecName)
	if lErr != nil {
		return true, lErr
	}
	if takeoverPending {
		return true, nil
	}

	knownFormerUser := child.Annotations[annotationSharedGroupDBDraining] == "true" || hasGroupSecret || hasLegacyGroupSecret || len(drainingPodIDs) > 0
	if knownFormerUser && !childIsReadyAtCurrentGeneration(child) {
		return true, nil
	}

	secNames := []string{sharedSecName}
	if hasLegacyGroupSecret {
		secNames = append(secNames, legacySecName)
	}
	terminatingLegacySec := ""
	if knownFormerUser && !hasLegacyGroupSecret {
		terminatingLegacySec = legacySecName
	}
	for _, ns := range namespaces {
		inUse, checkErr := namespaceWorkloadsStillUseSecret(ctx, targetClient, ns, child.Name, secNames, knownFormerUser, drainingPodIDs, terminatingLegacySec)
		if checkErr != nil {
			return true, checkErr
		}
		if inUse {
			return true, nil
		}
	}

	if drainingSec != nil {
		base := drainingSec.DeepCopy()
		delete(drainingSec.Annotations, annotationLegacyGroupDBDraining)
		delete(drainingSec.Annotations, annotationLegacyGroupDBOwner)
		if pErr := targetClient.Patch(ctx, drainingSec, client.MergeFrom(base)); pErr != nil && !apierrors.IsNotFound(pErr) {
			return true, fmt.Errorf("failed to clear legacy draining annotations on Secret %s/%s: %w", drainingSec.Namespace, drainingSec.Name, pErr)
		}
	}

	if child.Annotations[annotationSharedGroupDBDraining] == "true" {
		base := child.DeepCopy()
		delete(child.Annotations, annotationSharedGroupDBDraining)
		if pErr := r.Patch(ctx, child, client.MergeFrom(base)); pErr != nil && !apierrors.IsNotFound(pErr) {
			return true, fmt.Errorf("failed to clear draining annotation on child %s/%s: %w", child.Namespace, child.Name, pErr)
		}
	}
	return false, nil
}

func (r *PreviewGroupReconciler) legacyGroupSecretStillUsed(
	ctx context.Context,
	pgName string,
	child *divergeiov1alpha1.Environment,
	legacySecName string,
) (bool, error) {
	if r == nil || r.Client == nil || child == nil || legacySecName == "" {
		return false, nil
	}
	targetClient, namespaces, err := r.childTargetClientAndNamespaces(ctx, child)
	if err != nil {
		return true, err
	}
	hasLegacyGroupSecret, lErr := hasGroupOwnedSecretInNamespaces(ctx, targetClient, pgName, namespaces, legacySecName)
	if lErr != nil {
		return true, lErr
	}
	if !hasLegacyGroupSecret {
		return false, nil
	}
	for _, ns := range namespaces {
		inUse, checkErr := namespaceWorkloadsStillUseSecret(ctx, targetClient, ns, child.Name, []string{legacySecName}, false, nil, "")
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
		hasRecorded := hasRecordedSharedDBSecretTargets(pg)
		if hasPinned || hasRecorded {
			children, cErr := r.listChildEnvironments(ctx, pg)
			if cErr != nil {
				return nil, "", true, false, false, cErr
			}
			for i := range children {
				inUse, useErr := r.childOrWorkloadStillUsesSharedDBSecret(ctx, pg.Name, &children[i])
				if useErr != nil {
					return nil, "", true, false, false, useErr
				}
				if inUse {
					// Wait until live child Environments are updated AND their workloads/Pods have released the Secret before tearing down shared DB / Secrets.
					return nil, "", false, true, true, nil
				}
			}
			tdRequeue, err := r.teardownGroupDatabase(ctx, pg)
			if err != nil {
				return nil, "", true, false, false, err
			}
			if tdRequeue {
				return nil, "", false, true, true, nil
			}
			if hasPinned {
				desiredStatus := pg.Status.DeepCopy()
				baseCopy := pg.DeepCopy()
				delete(pg.Annotations, annotationGroupDBNamespace)
				delete(pg.Annotations, annotationGroupDBProvider)
				delete(pg.Annotations, annotationGroupDBMigrationHash)
				if err := r.Patch(ctx, pg, client.MergeFrom(baseCopy)); err != nil {
					pg.Status = *desiredStatus
					return nil, "", true, false, false, fmt.Errorf("failed to clear group database annotations: %w", err)
				}
				pg.Status = *desiredStatus
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
	targetClient, _, isRemote, err := helper.resolveTargetCluster(ctx, probeEnv)
	if err != nil {
		return "", err
	}
	if isRemote && effectiveClusterRef != nil {
		if rErr := r.recordSharedDBSecretTarget(ctx, pg, targetNS, effectiveClusterRef); rErr != nil {
			return "", rErr
		}
	}

	secretName := sharedGroupDBSecretName(childEnvName)
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
		sec.Labels[labelSharedGroupDBSecret] = "true"
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

func (r *PreviewGroupReconciler) teardownGroupDatabase(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) (bool, error) {
	hasPinned := pg != nil && pg.Annotations != nil &&
		(strings.TrimSpace(pg.Annotations[annotationGroupDBNamespace]) != "" ||
			strings.TrimSpace(pg.Annotations[annotationGroupDBProvider]) != "")
	hasRecordedTargets := hasRecordedSharedDBSecretTargets(pg)
	if !hasDedicatedGroupDatabase(pg) && !hasPinned && !hasRecordedTargets {
		return false, nil
	}

	baseTDCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	// Check and clean up any orphaned per-child shared DB Secrets BEFORE tearing down the
	// database provider, so a Running Pod on a disappeared child still blocks DB destruction.
	secRequeue, secErr := r.cleanupOrphanedChildSharedDBSecrets(baseTDCtx, pg, nil)
	if secErr != nil {
		return false, secErr
	}
	if secRequeue {
		return true, nil
	}

	if !hasDedicatedGroupDatabase(pg) && !hasPinned {
		return false, nil
	}

	anchorNS := resolveGroupDatabaseAnchorNamespace(pg)
	anchorEnv := r.buildGroupDatabaseAnchor(pg, anchorNS)
	helper := r.envProvisionHelper()

	tdCtx := baseTDCtx

	targetClient := r.Client
	target, tErr := helper.resolveTarget(tdCtx, anchorEnv)
	if tErr != nil {
		if multicluster.IsRemoteEnv(anchorEnv) {
			return false, fmt.Errorf("failed to resolve database target cluster for teardown: %w", tErr)
		}
	} else if target != nil {
		tdCtx = multicluster.WithTarget(baseTDCtx, target)
		if target.Client != nil {
			targetClient = target.Client
		}
	}

	dbProv, _, err := helper.resolveDatabaseProviderWithContext(tdCtx, anchorEnv, true)
	if err != nil {
		return false, err
	}
	if dbProv != nil && dbProv != r.DatabaseProvider {
		defer closeDatabaseProvider(dbProv)
	}
	if dbProv != nil && !isDefaultOrNoopDBProvider(dbProv) {
		if err := dbProv.Teardown(tdCtx, anchorEnv); err != nil {
			return false, err
		}
	}

	// Clean up any anchor migration/setup-sql DSN Secrets and Atlas CRs
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
	return false, errors.Join(errs...)
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
	keepSecrets := make(map[string]bool, (len(children)+len(desiredEnvNames))*2)
	deferredKeepSecrets := make(map[string]bool)
	var errs []error
	for i := range children {
		child := &children[i]
		childKey := child.Namespace + "/" + child.Name
		sharedSecName := sharedGroupDBSecretName(child.Name)
		legacySecName := legacySharedGroupDBSecretName(child.Name)
		sharedKey := child.Namespace + "/" + sharedSecName
		legacyKey := child.Namespace + "/" + legacySecName

		inUse, useErr := r.childOrWorkloadStillUsesSharedDBSecret(ctx, pg.Name, child)
		if useErr != nil {
			keepSecrets[sharedKey] = true
			keepSecrets[legacyKey] = true
			errs = append(errs, useErr)
			continue
		}
		if inUse {
			keepSecrets[sharedKey] = true
			if !sharedDesiredKeys[childKey] {
				keepSecrets[legacyKey] = true
				deferredKeepSecrets[sharedKey] = true
				deferredKeepSecrets[legacyKey] = true
			} else {
				legacyInUse, legErr := r.legacyGroupSecretStillUsed(ctx, pg.Name, child, legacySecName)
				if legErr != nil {
					keepSecrets[legacyKey] = true
					errs = append(errs, legErr)
				} else if legacyInUse {
					keepSecrets[legacyKey] = true
					deferredKeepSecrets[legacyKey] = true
				}
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
		keepSecrets[parts[0]+"/"+sharedGroupDBSecretName(parts[1])] = true
	}

	type recordedTargetRef struct {
		key string
		ns  string
	}
	type targetScan struct {
		client           client.Client
		namespaces       []string
		isRemote         bool
		recordedInactive []recordedTargetRef
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
	addRemoteScan := func(target *multicluster.Target, ns string, ref *divergeiov1alpha1.ClusterTargetRef) (int, bool) {
		if target == nil || !target.IsRemote || target.Client == nil {
			return -1, false
		}
		key := remoteScanKey(target, ns, ref)
		if idx, ok := seenTargets[key]; ok {
			if ns != "" && !containsString(scans[idx].namespaces, ns) {
				scans[idx].namespaces = append(scans[idx].namespaces, ns)
			}
			return idx, true
		}
		var nss []string
		if ns != "" {
			nss = []string{ns}
		}
		idx := len(scans)
		seenTargets[key] = idx
		scans = append(scans, targetScan{client: target.Client, namespaces: nss, isRemote: true})
		return idx, true
	}

	helper := r.envProvisionHelper()
	anchorNS := resolveGroupDatabaseAnchorNamespace(pg)
	activeRemoteTargetKeys := make(map[string]bool)
	if pg.Spec.ClusterRef != nil {
		activeRemoteTargetKeys[canonicalClusterRefKey(anchorNS, pg.Spec.ClusterRef)] = true
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
			activeRemoteTargetKeys[canonicalClusterRefKey(svcNS, effClusterRef)] = true
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
		activeRemoteTargetKeys[canonicalClusterRefKey(child.Namespace, child.Spec.Deploy.ClusterRef)] = true
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

	nonPrunableKeys := make(map[string]bool)
	for _, rt := range parseRecordedSharedDBSecretTargets(pg) {
		if rt.ClusterRef == nil {
			continue
		}
		normNS := strings.TrimSpace(rt.Namespace)
		if normNS == "" {
			normNS = "default"
		}
		key := canonicalClusterRefKey(normNS, rt.ClusterRef)
		probeRec := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: groupDatabaseAnchorName(pg.Name) + "-rec", Namespace: normNS},
			Spec:       divergeiov1alpha1.EnvironmentSpec{Deploy: divergeiov1alpha1.EnvironmentDeploy{Namespace: "same", ClusterRef: rt.ClusterRef}},
		}
		t, tErr := helper.resolveTarget(ctx, probeRec)
		if tErr != nil {
			if multicluster.IsRemoteEnv(probeRec) {
				errs = append(errs, fmt.Errorf("failed to resolve recorded spoke cluster for shared DB Secret cleanup: %w", tErr))
				nonPrunableKeys[key] = true
			}
			continue
		}
		if t != nil && t.IsRemote && t.Client != nil {
			if idx, ok := addRemoteScan(t, normNS, rt.ClusterRef); ok && !activeRemoteTargetKeys[key] {
				scans[idx].recordedInactive = append(scans[idx].recordedInactive, recordedTargetRef{key: key, ns: normNS})
			}
		} else if !activeRemoteTargetKeys[key] {
			scans[0].recordedInactive = append(scans[0].recordedInactive, recordedTargetRef{key: key, ns: normNS})
		}
	}

	matchLabels := client.MatchingLabels{
		labelPreviewGroup:           truncateLabel(pg.Name),
		"divergedev.com/managed-by": "diverge",
	}
	requeueNeeded := len(deferredKeepSecrets) > 0
	prunableKeys := make(map[string]bool)
	for _, scan := range scans {
		clusterListOK := false
		nsListOK := make(map[string]bool)
		remainingNS := make(map[string]bool)
		var secList corev1.SecretList
		if lErr := scan.client.List(ctx, &secList, matchLabels); lErr != nil {
			if meta.IsNoMatchError(lErr) || runtime.IsNotRegisteredError(lErr) {
				clusterListOK = true
			} else if scan.isRemote && apierrors.IsForbidden(lErr) && len(scan.namespaces) > 0 {
				for _, ns := range scan.namespaces {
					var nsList corev1.SecretList
					if nsErr := scan.client.List(ctx, &nsList, client.InNamespace(ns), matchLabels); nsErr != nil {
						if meta.IsNoMatchError(nsErr) || runtime.IsNotRegisteredError(nsErr) {
							nsListOK[ns] = true
							continue
						}
						errs = append(errs, fmt.Errorf("failed to list shared DB Secrets in %s: %w", ns, nsErr))
						continue
					}
					nsListOK[ns] = true
					secList.Items = append(secList.Items, nsList.Items...)
				}
			} else {
				errs = append(errs, fmt.Errorf("failed to list shared DB Secrets: %w", lErr))
				continue
			}
		} else {
			clusterListOK = true
		}
		for i := range secList.Items {
			sec := &secList.Items[i]
			if !isGroupOwnedSharedDBSecret(sec, pg.Name) {
				continue
			}
			secKey := sec.Namespace + "/" + sec.Name
			if keepSecrets[secKey] {
				remainingNS[sec.Namespace] = true
				if deferredKeepSecrets[secKey] {
					requeueNeeded = true
				}
				continue
			}
			inUse, checkErr := namespaceWorkloadsStillUseSecret(ctx, scan.client, sec.Namespace, "", []string{sec.Name}, false, nil, "")
			if checkErr != nil {
				remainingNS[sec.Namespace] = true
				errs = append(errs, checkErr)
				continue
			}
			if inUse {
				remainingNS[sec.Namespace] = true
				requeueNeeded = true
				continue
			}
			var delOpts []client.DeleteOption
			precond := client.Preconditions{}
			if sec.UID != "" {
				uid := sec.UID
				precond.UID = &uid
			}
			if sec.ResourceVersion != "" {
				rv := sec.ResourceVersion
				precond.ResourceVersion = &rv
			}
			if precond.UID != nil || precond.ResourceVersion != nil {
				delOpts = append(delOpts, precond)
			}
			dErr := scan.client.Delete(ctx, sec, delOpts...)
			if apierrors.IsConflict(dErr) {
				remainingNS[sec.Namespace] = true
				requeueNeeded = true
				continue
			}
			if dErr != nil && !apierrors.IsNotFound(dErr) {
				remainingNS[sec.Namespace] = true
				errs = append(errs, fmt.Errorf("failed to delete orphaned shared DB Secret %s/%s: %w", sec.Namespace, sec.Name, dErr))
			}
		}
		for _, rt := range scan.recordedInactive {
			if (clusterListOK || nsListOK[rt.ns]) && !remainingNS[rt.ns] && !nonPrunableKeys[rt.key] {
				prunableKeys[rt.key] = true
			}
		}
	}
	if len(prunableKeys) > 0 {
		if pErr := r.pruneRecordedSharedDBSecretTargets(ctx, pg, prunableKeys); pErr != nil {
			errs = append(errs, pErr)
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
