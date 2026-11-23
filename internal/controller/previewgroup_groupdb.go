package controller

import (
	"context"
	"crypto/sha256"
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
	annotationGroupDBNamespace = "divergedev.com/group-db-namespace"
	annotationGroupDBProvider  = "divergedev.com/group-db-provider"
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

// reconcileGroupDatabase provisions a single shared database for the PreviewGroup when Spec.Database is configured.
// Returns (sharedEnvVars, anchorNamespace, handledByGroup, ready, error).
// When no live DatabaseProvider is configured (e.g. pure unit tests without a DB provider), handledByGroup is false.
func (r *PreviewGroupReconciler) reconcileGroupDatabase(
	ctx context.Context,
	pg *divergeiov1alpha1.PreviewGroup,
) (envVars map[string]string, anchorNS string, handled bool, ready bool, retErr error) {
	if !hasDedicatedGroupDatabase(pg) {
		hasPinned := pg != nil && pg.Annotations != nil &&
			(strings.TrimSpace(pg.Annotations[annotationGroupDBNamespace]) != "" ||
				strings.TrimSpace(pg.Annotations[annotationGroupDBProvider]) != "")
		if hasPinned {
			if err := r.teardownGroupDatabase(ctx, pg); err != nil {
				return nil, "", true, false, err
			}
			baseCopy := pg.DeepCopy()
			delete(pg.Annotations, annotationGroupDBNamespace)
			delete(pg.Annotations, annotationGroupDBProvider)
			if err := r.Patch(ctx, pg, client.MergeFrom(baseCopy)); err != nil {
				return nil, "", true, false, fmt.Errorf("failed to clear group database annotations: %w", err)
			}
			return nil, "", true, true, nil
		}
		return nil, "", false, true, nil
	}

	anchorNS = resolveGroupDatabaseAnchorNamespace(pg)
	anchorEnv := r.buildGroupDatabaseAnchor(pg, anchorNS)
	helper := r.envProvisionHelper()

	target, err := helper.resolveTarget(ctx, anchorEnv)
	if err != nil {
		return nil, anchorNS, true, false, err
	}
	tCtx := ctx
	if target != nil {
		tCtx = multicluster.WithTarget(ctx, target)
	}

	provCtx, cancel := context.WithTimeout(tCtx, 20*time.Second)
	defer cancel()

	dbProv, resolvedName, err := helper.resolveDatabaseProviderWithContext(provCtx, anchorEnv, false)
	if err != nil {
		return nil, anchorNS, true, false, err
	}
	if dbProv != nil && dbProv != r.DatabaseProvider {
		defer closeDatabaseProvider(dbProv)
	}
	if dbProv == nil || isDefaultOrNoopDBProvider(dbProv) {
		// No concrete database provider configured; leave child spec intact for test/noop setups.
		return nil, anchorNS, false, true, nil
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
			return nil, anchorNS, true, false, fmt.Errorf("failed to persist group database metadata: %w", patchErr)
		}
	}

	dbRes, err := dbProv.Provision(provCtx, anchorEnv)
	if err != nil {
		return nil, anchorNS, true, false, err
	}
	if dbRes == nil || !dbRes.Ready {
		return nil, anchorNS, true, false, nil
	}

	if dbRes.SetupSQL != "" && !dbRes.SetupSQLExecuted {
		runner := r.SetupRunner
		if runner == nil {
			runner = &DirectSetupRunner{}
		}
		if setupErr := runner.RunSetupSQL(tCtx, anchorEnv, dbRes.SetupSQL, dbRes.AdminDSN); setupErr != nil {
			if errors.Is(setupErr, ErrHookInProgress) {
				return nil, anchorNS, true, false, nil
			}
			return nil, anchorNS, true, false, setupErr
		}
	}

	// Execute group-level MigrationJob / Atlas ONCE against the shared database rather than spawning N racing child Jobs.
	if pg.Spec.Database != nil && (pg.Spec.Database.MigrationJob != nil || pg.Spec.Database.Atlas != nil) {
		if migErr := helper.runMigrations(tCtx, anchorEnv, dbRes); migErr != nil {
			if errors.Is(migErr, ErrHookInProgress) {
				return nil, anchorNS, true, false, nil
			}
			return nil, anchorNS, true, false, migErr
		}
	}

	outVars := make(map[string]string, len(dbRes.EnvVars)+1)
	for k, v := range dbRes.EnvVars {
		outVars[k] = v
	}
	if len(outVars) == 0 && strings.TrimSpace(dbRes.DSN) != "" {
		outVars["DATABASE_URL"] = dbRes.DSN
	}
	return outVars, anchorNS, true, true, nil
}

func rewriteGroupDBVarsForNamespace(baseVars map[string]string, anchorEnvName, anchorNS, childNS string) map[string]string {
	out := make(map[string]string, len(baseVars))
	clusterSvc := cnpg.ClusterName(anchorEnvName) + "-rw"
	shortSvc := fmt.Sprintf("%s.%s.svc", clusterSvc, anchorNS)
	qualifiedSvc := fmt.Sprintf("%s.%s.svc.cluster.local", clusterSvc, anchorNS)
	crossNS := anchorNS != "" && childNS != "" && anchorNS != childNS

	for k, v := range baseVars {
		if crossNS {
			if parsed, err := url.Parse(v); err == nil && parsed.Host != "" {
				host := parsed.Hostname()
				if host == clusterSvc || host == shortSvc {
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
	if dbProv == nil || isDefaultOrNoopDBProvider(dbProv) {
		return nil
	}
	return dbProv.Teardown(tdCtx, anchorEnv)
}

// syncMemberStandaloneEnvironments hibernates standalone PR Environments that are actively attached to pg
// (within the same authorized service namespace) and wakes any previously attached Environments that left pg.
func (r *PreviewGroupReconciler) syncMemberStandaloneEnvironments(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) {
	if pg == nil {
		return
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
		claimed[envNS+"/"+envName] = true

		var standalone divergeiov1alpha1.Environment
		if err := r.Get(ctx, types.NamespacedName{Namespace: envNS, Name: envName}, &standalone); err != nil {
			continue
		}
		if !standalone.DeletionTimestamp.IsZero() || standalone.Labels[labelManagedBy] == "diverge-previewgroup" {
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
		standalone.Annotations[metrics.AnnotationHibernated] = "true"
		if standalone.Annotations[metrics.AnnotationHibernatedAt] == "" {
			standalone.Annotations[metrics.AnnotationHibernatedAt] = nowRFC3339
		}
		standalone.Annotations[previewgroup.AnnotationAttachedPreviewGroup] = pg.Name
		_ = r.Patch(ctx, &standalone, client.MergeFrom(base))
	}

	r.wakeUnclaimedStandaloneEnvironments(ctx, pg.Name, claimed)
}

// wakeUnclaimedStandaloneEnvironments wakes standalone Environments whose attached-previewgroup annotation
// points to groupName when they are no longer claimed by groupName (or re-points them if another active group claims them).
func (r *PreviewGroupReconciler) wakeUnclaimedStandaloneEnvironments(ctx context.Context, groupName string, stillClaimed map[string]bool) {
	if strings.TrimSpace(groupName) == "" {
		return
	}
	var envList divergeiov1alpha1.EnvironmentList
	if err := r.List(ctx, &envList); err != nil {
		return
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
		return
	}

	// Check whether any other active PreviewGroup still claims each candidate Environment.
	otherOwner := make(map[string]string)
	var pgList divergeiov1alpha1.PreviewGroupList
	if err := r.List(ctx, &pgList); err == nil {
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
					envNS = ns
				}
				if envNS == ns {
					otherOwner[envNS+"/"+m.EnvironmentName] = other.Name
				}
			}
		}
	}

	for _, env := range candidates {
		base := env.DeepCopy()
		if env.Annotations == nil {
			continue
		}
		if replacementGroup := otherOwner[env.Namespace+"/"+env.Name]; replacementGroup != "" {
			env.Annotations[previewgroup.AnnotationAttachedPreviewGroup] = replacementGroup
		} else {
			delete(env.Annotations, metrics.AnnotationHibernated)
			delete(env.Annotations, previewgroup.AnnotationAttachedPreviewGroup)
		}
		if !apierrors.IsNotFound(r.Patch(ctx, env, client.MergeFrom(base))) {
			continue
		}
	}
}
