package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

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

// reconcileGroupDatabase provisions a single shared database for the PreviewGroup when Spec.Database is configured.
// Returns (sharedEnvVars, anchorNamespace, handledByGroup, ready, cleanupPendingRequeue, error).
func (r *PreviewGroupReconciler) reconcileGroupDatabase(
	ctx context.Context,
	pg *divergeiov1alpha1.PreviewGroup,
) (envVars map[string]string, anchorNS string, handled bool, ready bool, cleanupPending bool, retErr error) {
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
				if children[i].Annotations[annotationSharedGroupDB] == "true" {
					// Switch live child Environments off the shared DB in this reconcile pass first, then tear down on requeue.
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

	// Execute group-level MigrationJob / Atlas ONCE per digest against the shared database (recording completion durably across Job TTL GC).
	if pg.Spec.Database != nil && (pg.Spec.Database.MigrationJob != nil || pg.Spec.Database.Atlas != nil) {
		migDigest := groupDatabaseMigrationDigest(pg, anchorEnv.Name, anchorNS)
		if pg.Annotations[annotationGroupDBMigrationHash] != migDigest {
			if migErr := helper.runMigrations(tCtx, anchorEnv, dbRes); migErr != nil {
				if errors.Is(migErr, ErrHookInProgress) {
					return nil, anchorNS, true, false, false, nil
				}
				return nil, anchorNS, true, false, false, migErr
			}
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
	return outVars, anchorNS, true, true, false, nil
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
		!divergeiov1alpha1.EqualClusterTargetRef(pg.Spec.ClusterRef, effectiveClusterRef, targetNS) {
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

	tdCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if target, tErr := helper.resolveTarget(tdCtx, anchorEnv); tErr == nil && target != nil {
		tdCtx = multicluster.WithTarget(tdCtx, target)
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

	// Clean up any anchor migration/setup-sql DSN Secrets and Atlas CRs created without an Environment UID
	targetClient := r.Client
	if tClient, _, _, tErr := helper.resolveTargetCluster(tdCtx, anchorEnv); tErr == nil && tClient != nil {
		targetClient = tClient
	}
	for _, labelVal := range []string{truncateLabel(anchorEnv.Name), truncateLabel(anchorEnv.Name + "-admin")} {
		var secList corev1.SecretList
		if lErr := targetClient.List(tdCtx, &secList, client.InNamespace(anchorNS), client.MatchingLabels{
			labelEnvironment: labelVal,
		}); lErr == nil {
			for i := range secList.Items {
				hk := secList.Items[i].Labels[labelHookType]
				if hk == hookTypeMigration || hk == hookTypeSetupSQL {
					_ = client.IgnoreNotFound(targetClient.Delete(tdCtx, &secList.Items[i]))
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
		_ = client.IgnoreNotFound(targetClient.Delete(tdCtx, u))
	}
	return nil
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
	for i := range envList.Items {
		env := &envList.Items[i]
		if !env.DeletionTimestamp.IsZero() || env.Labels[labelManagedBy] == "diverge-previewgroup" {
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
		svcNS := make(map[string]string, len(other.Spec.Services))
		for _, s := range other.Spec.Services {
			if s.Mode == divergeiov1alpha1.ServiceModeBaseline {
				continue
			}
			ns := strings.TrimSpace(s.Namespace)
			if ns == "" {
				ns = "default"
			}
			svcNS[s.Name] = ns
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
			if envNS == ns {
				if cand, exists := candidateByKey[envNS+"/"+m.EnvironmentName]; exists && previewgroup.MemberMatchesEnvironmentSource(m, cand) {
					otherOwner[envNS+"/"+m.EnvironmentName] = other.Name
				}
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
