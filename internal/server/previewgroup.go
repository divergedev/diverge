package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/previewgroup"
	"github.com/divergedev/diverge/internal/server/auth"
	"github.com/divergedev/diverge/internal/server/streaming"
)

// IfMatchHeader allows callers (Web UI, MCP) to guard DeletePreviewGroup against concurrent modifications.
const IfMatchHeader = "If-Match"

type PreviewGroupService struct {
	client      client.Client
	k8sClient   kubernetes.Interface
	informerMgr *streaming.InformerManager
	limiter     *StreamLimiter
	logger      *slog.Logger
	auditLogger *AuditLogger
}

func NewPreviewGroupService(c client.Client, k8s kubernetes.Interface, informerMgr *streaming.InformerManager, limiter *StreamLimiter, logger *slog.Logger, audit *AuditLogger) divergev1alpha1connect.PreviewGroupServiceHandler {
	return &PreviewGroupService{
		client:      c,
		k8sClient:   k8s,
		informerMgr: informerMgr,
		limiter:     limiter,
		logger:      logger,
		auditLogger: audit,
	}
}

func effectiveSvcNamespace(ns string) string {
	trimmed := strings.TrimSpace(ns)
	if trimmed == "" {
		return "default"
	}
	return trimmed
}

func previewGroupMatchesNamespace(pg *v1alpha1.PreviewGroup, namespace string) bool {
	if pg == nil || namespace == "" {
		return true
	}
	if pg.Namespace == namespace {
		return true
	}
	// Cluster-scoped PreviewGroup (pg.Namespace == ""): match if any service targets namespace.
	if pg.Namespace == "" {
		for _, svc := range pg.Spec.Services {
			if effectiveSvcNamespace(svc.Namespace) == namespace {
				return true
			}
		}
	}
	return false
}

func previewGroupAllowedForMacaroon(pg *v1alpha1.PreviewGroup, boundNS string) bool {
	if pg == nil || boundNS == "" {
		return false
	}
	if len(pg.Spec.Services) == 0 {
		return effectiveSvcNamespace(pg.Namespace) == boundNS
	}
	for _, svc := range pg.Spec.Services {
		if effectiveSvcNamespace(svc.Namespace) != boundNS {
			return false
		}
	}
	return true
}

func validateUniqueGroupMembers(pg *v1alpha1.PreviewGroup) error {
	if pg == nil {
		return nil
	}
	seen := make(map[string]bool)
	for _, m := range previewgroup.ParseMembers(pg) {
		svc := strings.TrimSpace(m.Service)
		if svc == "" {
			continue
		}
		if seen[svc] {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("duplicate service %q in divergedev.com/group-members annotation", svc))
		}
		seen[svc] = true
	}
	return nil
}

func enforcePreviewGroupValidation(prior, current *v1alpha1.PreviewGroup) error {
	if current != nil {
		if err := validateUniqueGroupMembers(current); err != nil {
			return err
		}
		for _, svc := range current.Spec.Services {
			if strings.TrimSpace(svc.Name) == "" {
				return connect.NewError(connect.CodeInvalidArgument, errors.New("service name is required"))
			}
			if svc.Mode == v1alpha1.ServiceModeImage && strings.TrimSpace(svc.Image) == "" {
				return connect.NewError(connect.CodeInvalidArgument, errors.New(`image is required when mode is "image"`))
			}
		}
	}
	errs := v1alpha1.ValidatePreviewGroupUpdate(prior, current)
	hasServices := current != nil && len(current.Spec.Services) > 0
	for _, fe := range errs {
		if fe == nil {
			continue
		}
		if (hasServices && strings.HasPrefix(fe.Field, "spec.services") && fe.Type != field.ErrorTypeRequired) ||
			strings.Contains(fe.Field, "clusterRef") ||
			fe.Type == field.ErrorTypeForbidden ||
			fe.Type == field.ErrorTypeDuplicate ||
			strings.HasSuffix(fe.Field, ".name") ||
			strings.HasSuffix(fe.Field, ".pathPrefix") ||
			strings.HasSuffix(fe.Field, ".endpoint") {
			return connect.NewError(connect.CodeInvalidArgument, errors.New(fe.Detail))
		}
	}
	return nil
}

func (s *PreviewGroupService) CreatePreviewGroup(ctx context.Context, req *connect.Request[pb.CreatePreviewGroupRequest]) (*connect.Response[pb.CreatePreviewGroupResponse], error) {
	msg := req.Msg
	if msg.PreviewGroup == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("preview group is required"))
	}

	rawNS := strings.TrimSpace(msg.Namespace)
	isClusterRequest := rawNS == "_cluster"
	namespace := rawNS
	if namespace == "" || isClusterRequest {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}

	if msg.PreviewGroup.Name != "" {
		if err := ValidateDNS1123Label(msg.PreviewGroup.Name, "name"); err != nil {
			return nil, err
		}
	}
	if msg.PreviewGroup.Namespace != "" && msg.PreviewGroup.Namespace != "_cluster" && !isClusterRequest {
		if err := ValidateNamespaceMatch(namespace, msg.PreviewGroup.Namespace); err != nil {
			return nil, err
		}
	}

	// RBAC check on primary namespace + every target service namespace (defaulting empty svc.Namespace to "default")
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "create", namespace, "previewgroups"); err != nil {
		return nil, err
	}
	if err := s.authorizeCreateServiceNamespaces(ctx, namespace, msg.PreviewGroup.GetSpec(), msg.PreviewGroup.GetAnnotations()); err != nil {
		return nil, err
	}

	realCrd, err := ProtoPgToCRD(msg.PreviewGroup)
	if err != nil {
		if strings.Contains(err.Error(), "invalid label") || strings.Contains(err.Error(), "invalid annotation") {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal server error"))
	}

	if isClusterRequest {
		realCrd.Namespace = ""
	} else {
		realCrd.Namespace = namespace
	}

	if err := enforcePreviewGroupValidation(nil, realCrd); err != nil {
		return nil, err
	}

	if err := s.client.Create(ctx, realCrd); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	s.auditLogger.LogMutation(ctx, "resource.created", "previewgroup", realCrd.Name, realCrd.Namespace)

	domBack, err := CRDPgToProto(realCrd)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal server error"))
	}
	return connect.NewResponse(&pb.CreatePreviewGroupResponse{
		PreviewGroup: domBack,
	}), nil
}

func (s *PreviewGroupService) authorizeCreateServiceNamespaces(ctx context.Context, primaryNS string, spec *pb.PreviewGroupSpec, annotations map[string]string) error {
	checked := make(map[string]bool)
	checkNS := func(ns string) error {
		effNS := effectiveSvcNamespace(ns)
		if checked[effNS] {
			return nil
		}
		if err := ValidateDNS1123Label(effNS, "service.namespace"); err != nil {
			return err
		}
		if err := AuthorizePreviewGroupServiceNamespace(ctx, s.k8sClient, s.auditLogger, "create", effNS, primaryNS); err != nil {
			return err
		}
		checked[effNS] = true
		return nil
	}
	if spec != nil {
		for _, svc := range spec.GetServices() {
			if err := checkNS(svc.GetNamespace()); err != nil {
				return err
			}
		}
	}
	for _, m := range previewgroup.ParseMembersFromAnnotations(annotations) {
		if m.Namespace != "" {
			if err := checkNS(m.Namespace); err != nil {
				return err
			}
		}
		if m.EnvironmentName != "" {
			envNS := m.EnvironmentNamespace
			if envNS == "" {
				envNS = m.Namespace
			}
			if err := checkNS(envNS); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *PreviewGroupService) authorizeReadGroup(ctx context.Context, authzNS string, crd *v1alpha1.PreviewGroup) error {
	user, ok := auth.UserInfoFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}
	// Macaroon tokens are strictly confined to a single sandbox namespace: every service in the group must match it.
	if isMacaroonUser(user) {
		if len(crd.Spec.Services) == 0 {
			return AuthorizePreviewGroupServiceNamespace(ctx, s.k8sClient, s.auditLogger, "get", effectiveSvcNamespace(crd.Namespace), authzNS)
		}
		for _, svc := range crd.Spec.Services {
			effNS := effectiveSvcNamespace(svc.Namespace)
			if err := AuthorizePreviewGroupServiceNamespace(ctx, s.k8sClient, s.auditLogger, "get", effNS, authzNS); err != nil {
				return err
			}
		}
		return nil
	}
	if len(crd.Spec.Services) == 0 {
		targetNS := effectiveSvcNamespace(crd.Namespace)
		if authzNS != "" && targetNS == authzNS {
			return nil
		}
		return AuthorizePreviewGroupServiceNamespace(ctx, s.k8sClient, s.auditLogger, "get", targetNS, authzNS)
	}
	if authzNS != "" && crd.Namespace == authzNS {
		return nil
	}
	// Collaborators with read access to ANY participating service namespace may inspect the shared group.
	var lastDenied error
	for _, svc := range crd.Spec.Services {
		effNS := effectiveSvcNamespace(svc.Namespace)
		err := AuthorizePreviewGroupServiceNamespace(ctx, s.k8sClient, s.auditLogger, "get", effNS, authzNS)
		if err == nil {
			return nil
		}
		lastDenied = err
	}
	if lastDenied != nil {
		return lastDenied
	}
	return nil
}

func nonMemberAnnotationsChanged(a, b map[string]string) bool {
	keys := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	for k := range keys {
		if k == previewgroup.AnnotationGroupMembers || k == "divergedev.com/traceparent" {
			continue
		}
		if a[k] != b[k] {
			return true
		}
	}
	return false
}

func parseDecimalRV(rv string) (uint64, bool) {
	trimmed := strings.TrimSpace(rv)
	if trimmed == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func resolveMemberEnvNS(m previewgroup.MemberRecord) string {
	if ns := strings.TrimSpace(m.EnvironmentNamespace); ns != "" {
		return ns
	}
	return strings.TrimSpace(m.Namespace)
}

func isSafeSourcePromotion(existing, updated *v1alpha1.PreviewGroup) bool {
	if equality.Semantic.DeepEqual(existing.Spec.Source, updated.Spec.Source) {
		return true
	}
	// Only exempt Spec.Source promotion/clearing when the prior creator MR is no longer present in updated members
	// and updated.Spec.Source matches a surviving MR member (or clears to local when no MR member remains).
	if existing.Spec.Source.MR <= 0 {
		return false
	}
	if updated.Spec.Source.CommitSHA != "" && updated.Spec.Source.CommitSHA != existing.Spec.Source.CommitSHA {
		return false
	}
	exProv := strings.ToLower(strings.TrimSpace(existing.Spec.Source.Provider))
	if exProv == "local" {
		exProv = ""
	}
	updatedMembers := previewgroup.ParseMembers(updated)
	hasPositiveMRMember := false
	for _, m := range updatedMembers {
		if m.MR > 0 {
			hasPositiveMRMember = true
		}
		mProv := strings.ToLower(strings.TrimSpace(m.Provider))
		if mProv == "local" {
			mProv = ""
		}
		if m.MR == existing.Spec.Source.MR &&
			(exProv == "" || mProv == "" || mProv == exProv) &&
			(existing.Spec.Source.Project == "" || strings.EqualFold(strings.TrimSpace(m.Project), strings.TrimSpace(existing.Spec.Source.Project))) {
			return false
		}
	}
	if hasPositiveMRMember {
		if updated.Spec.Source.CommitSHA != "" {
			return false
		}
		for _, m := range updatedMembers {
			if m.MR <= 0 {
				continue
			}
			mProv := strings.ToLower(strings.TrimSpace(m.Provider))
			if mProv != "github" && mProv != "gitlab" {
				mProv = "local"
			}
			mBranch := strings.TrimSpace(m.Branch)
			if strings.EqualFold(updated.Spec.Source.Provider, mProv) &&
				strings.EqualFold(strings.TrimSpace(m.Project), strings.TrimSpace(updated.Spec.Source.Project)) &&
				m.MR == updated.Spec.Source.MR &&
				(updated.Spec.Source.Branch == mBranch || (mBranch == "" && updated.Spec.Source.Branch == updated.Name)) {
				return true
			}
		}
		return false
	}
	return strings.EqualFold(strings.TrimSpace(updated.Spec.Source.Provider), "local") &&
		strings.TrimSpace(updated.Spec.Source.Project) == "" &&
		updated.Spec.Source.MR == 0 &&
		updated.Spec.Source.CommitSHA == "" &&
		(updated.Spec.Source.Branch == updated.Name || updated.Spec.Source.Branch == existing.Name)
}

func (s *PreviewGroupService) authorizeUpdateGroup(ctx context.Context, authzNS string, existing, updated *v1alpha1.PreviewGroup, specTouched bool) error {
	user, ok := auth.UserInfoFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	checked := make(map[string]bool)
	checkVerbNS := func(verb, ns string) error {
		effNS := effectiveSvcNamespace(ns)
		if err := ValidateDNS1123Label(effNS, "service.namespace"); err != nil {
			return err
		}
		key := verb + ":" + effNS
		if checked[key] {
			return nil
		}
		if err := AuthorizePreviewGroupServiceNamespace(ctx, s.k8sClient, s.auditLogger, verb, effNS, authzNS); err != nil {
			return err
		}
		checked[key] = true
		return nil
	}

	// Reject duplicate member records upfront so map-keyed authorization and downstream slice consumers observe identical membership.
	if err := validateUniqueGroupMembers(updated); err != nil {
		return err
	}

	// Macaroon capability tokens may only update PreviewGroups whose existing AND updated services/members all stay in boundNS.
	if isMacaroonUser(user) {
		if len(existing.Spec.Services) == 0 && len(updated.Spec.Services) == 0 {
			if err := checkVerbNS("update", existing.Namespace); err != nil {
				return err
			}
		}
		for _, svc := range existing.Spec.Services {
			if err := checkVerbNS("update", svc.Namespace); err != nil {
				return err
			}
		}
		for _, svc := range updated.Spec.Services {
			if err := checkVerbNS("update", svc.Namespace); err != nil {
				return err
			}
		}
		for _, m := range previewgroup.ParseMembers(updated) {
			if m.Namespace != "" {
				if err := checkVerbNS("update", m.Namespace); err != nil {
					return err
				}
			}
			if m.EnvironmentNamespace != "" || strings.TrimSpace(m.EnvironmentName) != "" {
				if err := checkVerbNS("update", resolveMemberEnvNS(m)); err != nil {
					return err
				}
			}
		}
		for _, m := range previewgroup.ParseMembers(existing) {
			if m.Namespace != "" {
				if err := checkVerbNS("update", m.Namespace); err != nil {
					return err
				}
			}
			if m.EnvironmentNamespace != "" || strings.TrimSpace(m.EnvironmentName) != "" {
				if err := checkVerbNS("update", resolveMemberEnvNS(m)); err != nil {
					return err
				}
			}
		}
		return nil
	}

	existingByName := make(map[string]v1alpha1.PreviewGroupServiceSpec, len(existing.Spec.Services))
	for _, svc := range existing.Spec.Services {
		existingByName[svc.Name] = svc
	}
	updatedByName := make(map[string]v1alpha1.PreviewGroupServiceSpec, len(updated.Spec.Services))
	for _, svc := range updated.Spec.Services {
		effNS := effectiveSvcNamespace(svc.Namespace)
		if err := ValidateDNS1123Label(effNS, "service.namespace"); err != nil {
			return err
		}
		updatedByName[svc.Name] = svc
	}

	// Authorize any additions, modifications, or removals in divergedev.com/group-members across both old and new namespaces.
	authorizeMemberDiff := func() error {
		oldMembers := make(map[string]previewgroup.MemberRecord)
		for _, m := range previewgroup.ParseMembers(existing) {
			oldMembers[m.Service] = m
		}
		newMembers := make(map[string]previewgroup.MemberRecord)
		for _, m := range previewgroup.ParseMembers(updated) {
			newMembers[m.Service] = m
		}
		resolveMemberNS := func(svcName string, m previewgroup.MemberRecord) string {
			if u, ok := updatedByName[svcName]; ok && strings.TrimSpace(u.Namespace) != "" {
				return u.Namespace
			}
			if e, ok := existingByName[svcName]; ok && strings.TrimSpace(e.Namespace) != "" {
				return e.Namespace
			}
			if strings.TrimSpace(m.Namespace) != "" {
				return m.Namespace
			}
			return m.EnvironmentNamespace
		}
		for svcName, oldM := range oldMembers {
			newM, stillMember := newMembers[svcName]
			if !stillMember {
				verb := "delete"
				if _, svcStillExists := updatedByName[svcName]; svcStillExists {
					verb = "update"
				}
				if err := checkVerbNS(verb, resolveMemberNS(svcName, oldM)); err != nil {
					return err
				}
				if oldM.Namespace != "" {
					if err := checkVerbNS(verb, oldM.Namespace); err != nil {
						return err
					}
				}
				if oldM.EnvironmentNamespace != "" || strings.TrimSpace(oldM.EnvironmentName) != "" {
					if err := checkVerbNS(verb, resolveMemberEnvNS(oldM)); err != nil {
						return err
					}
				}
				continue
			}
			if !equality.Semantic.DeepEqual(oldM, newM) {
				if err := checkVerbNS("update", resolveMemberNS(svcName, oldM)); err != nil {
					return err
				}
				if err := checkVerbNS("update", resolveMemberNS(svcName, newM)); err != nil {
					return err
				}
				if oldM.Namespace != "" {
					if err := checkVerbNS("update", oldM.Namespace); err != nil {
						return err
					}
				}
				if oldM.EnvironmentNamespace != "" || strings.TrimSpace(oldM.EnvironmentName) != "" {
					if err := checkVerbNS("update", resolveMemberEnvNS(oldM)); err != nil {
						return err
					}
				}
				if newM.Namespace != "" {
					if err := checkVerbNS("update", newM.Namespace); err != nil {
						return err
					}
				}
				if newM.EnvironmentNamespace != "" || strings.TrimSpace(newM.EnvironmentName) != "" {
					if err := checkVerbNS("update", resolveMemberEnvNS(newM)); err != nil {
						return err
					}
				}
			}
		}
		for svcName, newM := range newMembers {
			if _, existed := oldMembers[svcName]; !existed {
				verb := "create"
				if _, svcExisted := existingByName[svcName]; svcExisted {
					verb = "update"
				}
				if err := checkVerbNS(verb, resolveMemberNS(svcName, newM)); err != nil {
					return err
				}
				if newM.Namespace != "" {
					if err := checkVerbNS(verb, newM.Namespace); err != nil {
						return err
					}
				}
				if newM.EnvironmentNamespace != "" || strings.TrimSpace(newM.EnvironmentName) != "" {
					if err := checkVerbNS(verb, resolveMemberEnvNS(newM)); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}

	if err := authorizeMemberDiff(); err != nil {
		return err
	}

	if !specTouched {
		// Metadata-only update: if labels or non-member annotations changed (or no member diff covered a namespace),
		// require update permission across all existing service namespaces.
		if !equality.Semantic.DeepEqual(existing.Labels, updated.Labels) ||
			nonMemberAnnotationsChanged(existing.Annotations, updated.Annotations) ||
			existing.Annotations[previewgroup.AnnotationGroupMembers] == updated.Annotations[previewgroup.AnnotationGroupMembers] {
			if len(existing.Spec.Services) == 0 {
				return checkVerbNS("update", existing.Namespace)
			}
			for _, svc := range existing.Spec.Services {
				if err := checkVerbNS("update", svc.Namespace); err != nil {
					return err
				}
			}
		}
		return nil
	}

	groupGlobalsChanged := !equality.Semantic.DeepEqual(existing.Spec.Routing, updated.Spec.Routing) ||
		!equality.Semantic.DeepEqual(existing.Spec.Database, updated.Spec.Database) ||
		!equality.Semantic.DeepEqual(existing.Spec.ClusterRef, updated.Spec.ClusterRef) ||
		!equality.Semantic.DeepEqual(existing.Spec.Lifecycle, updated.Spec.Lifecycle) ||
		!equality.Semantic.DeepEqual(existing.Spec.TopologyOverrides, updated.Spec.TopologyOverrides) ||
		existing.Spec.Owner != updated.Spec.Owner ||
		!isSafeSourcePromotion(existing, updated) ||
		!equality.Semantic.DeepEqual(existing.Labels, updated.Labels) ||
		nonMemberAnnotationsChanged(existing.Annotations, updated.Annotations)

	if len(existing.Spec.Services) == 0 && len(updated.Spec.Services) == 0 {
		return checkVerbNS("update", existing.Namespace)
	}

	// Check removed and modified existing services
	for name, oldSvc := range existingByName {
		oldNS := effectiveSvcNamespace(oldSvc.Namespace)
		newSvc, stillPresent := updatedByName[name]
		if !stillPresent {
			if err := checkVerbNS("delete", oldNS); err != nil {
				return err
			}
			continue
		}
		newNS := effectiveSvcNamespace(newSvc.Namespace)
		if oldNS != newNS {
			if err := checkVerbNS("delete", oldNS); err != nil {
				return err
			}
			if err := checkVerbNS("create", newNS); err != nil {
				return err
			}
		} else if groupGlobalsChanged || !equality.Semantic.DeepEqual(oldSvc, newSvc) {
			if err := checkVerbNS("update", oldNS); err != nil {
				return err
			}
		}
	}

	// Check newly added services
	for name, newSvc := range updatedByName {
		if _, existed := existingByName[name]; !existed {
			newNS := effectiveSvcNamespace(newSvc.Namespace)
			if err := checkVerbNS("create", newNS); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *PreviewGroupService) authorizeDeleteGroup(ctx context.Context, authzNS string, crd *v1alpha1.PreviewGroup) error {
	checked := make(map[string]bool)
	if crd.Namespace != "" && crd.Namespace != authzNS {
		if err := AuthorizePreviewGroupServiceNamespace(ctx, s.k8sClient, s.auditLogger, "delete", crd.Namespace, authzNS); err != nil {
			return err
		}
		checked[crd.Namespace] = true
	}
	if len(crd.Spec.Services) == 0 {
		effNS := effectiveSvcNamespace(crd.Namespace)
		if !checked[effNS] {
			if err := AuthorizePreviewGroupServiceNamespace(ctx, s.k8sClient, s.auditLogger, "delete", effNS, authzNS); err != nil {
				return err
			}
		}
		return nil
	}
	for _, svc := range crd.Spec.Services {
		effNS := effectiveSvcNamespace(svc.Namespace)
		if checked[effNS] {
			continue
		}
		if err := AuthorizePreviewGroupServiceNamespace(ctx, s.k8sClient, s.auditLogger, "delete", effNS, authzNS); err != nil {
			return err
		}
		checked[effNS] = true
	}
	return nil
}

func normalizePreviewGroupNamespace(raw string) (lookupNS, authzNS string, explicitNS bool, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "_cluster" {
		return "", "", false, nil
	}
	if err := ValidateDNS1123Label(trimmed, "namespace"); err != nil {
		return "", "", false, err
	}
	return trimmed, trimmed, true, nil
}

func (s *PreviewGroupService) authorizePreLookup(ctx context.Context, verb, authzNS string, explicitNS bool) (string, error) {
	user, ok := auth.UserInfoFromContext(ctx)
	if !ok {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}
	if isMacaroonUser(user) {
		targetNS := authzNS
		if targetNS == "" {
			if bound := user.Extra[auth.ExtraKeyNamespace]; len(bound) > 0 {
				targetNS = strings.TrimSpace(bound[0])
			}
		}
		if targetNS == "" {
			targetNS = "default"
		}
		if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, verb, targetNS, "previewgroups"); err != nil {
			return "", err
		}
		return targetNS, nil
	}
	if explicitNS {
		if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, verb, authzNS, "previewgroups"); err != nil {
			return "", err
		}
		return authzNS, nil
	}
	// Implicit cluster-scoped lookup (e.g. Web UI /preview-groups/<name>): per-service namespace authorization
	// is enforced against the loaded PreviewGroup's Spec.Services immediately after lookup.
	return "", nil
}

func (s *PreviewGroupService) authorizeNotFoundFallback(ctx context.Context, verb, authzNS string) error {
	if authzNS != "" {
		return nil
	}
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, verb, "", "previewgroups"); err == nil {
		return nil
	}
	return AuthorizeAction(ctx, s.k8sClient, s.auditLogger, verb, "default", "previewgroups")
}

func (s *PreviewGroupService) getPreviewGroupCRD(ctx context.Context, name, lookupNS string, out *v1alpha1.PreviewGroup) error {
	err := s.client.Get(ctx, client.ObjectKey{Name: name, Namespace: lookupNS}, out)
	if err != nil && apierrors.IsNotFound(err) {
		altNS := ""
		if lookupNS == "" {
			altNS = "default"
		}
		if clusterErr := s.client.Get(ctx, client.ObjectKey{Name: name, Namespace: altNS}, out); clusterErr == nil {
			return nil
		}
		if lookupNS == "" {
			var list v1alpha1.PreviewGroupList
			if listErr := s.client.List(ctx, &list); listErr == nil {
				for i := range list.Items {
					if list.Items[i].Name == name {
						*out = *list.Items[i].DeepCopy()
						return nil
					}
				}
			}
		}
	}
	return err
}

func (s *PreviewGroupService) GetPreviewGroup(ctx context.Context, req *connect.Request[pb.GetPreviewGroupRequest]) (*connect.Response[pb.GetPreviewGroupResponse], error) {
	if err := ValidateDNS1123Label(req.Msg.Name, "name"); err != nil {
		return nil, err
	}
	lookupNS, rawAuthzNS, explicitNS, err := normalizePreviewGroupNamespace(req.Msg.Namespace)
	if err != nil {
		return nil, err
	}

	authzNS, err := s.authorizePreLookup(ctx, "get", rawAuthzNS, explicitNS)
	if err != nil {
		return nil, err
	}

	var crd v1alpha1.PreviewGroup
	if err := s.getPreviewGroupCRD(ctx, req.Msg.Name, lookupNS, &crd); err != nil {
		if apierrors.IsNotFound(err) {
			if fbErr := s.authorizeNotFoundFallback(ctx, "get", authzNS); fbErr != nil {
				return nil, fbErr
			}
		}
		return nil, SanitizeK8sError(s.logger, err)
	}
	if err := s.authorizeReadGroup(ctx, authzNS, &crd); err != nil {
		return nil, err
	}
	dom, err := CRDPgToProto(&crd)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal server error"))
	}
	return connect.NewResponse(&pb.GetPreviewGroupResponse{
		PreviewGroup: dom,
	}), nil
}

func (s *PreviewGroupService) ListPreviewGroups(ctx context.Context, req *connect.Request[pb.ListPreviewGroupsRequest]) (*connect.Response[pb.ListPreviewGroupsResponse], error) {
	namespace := strings.TrimSpace(req.Msg.Namespace)
	if namespace != "" {
		if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
			return nil, err
		}
	}

	const maxPageTokenLen = 4096
	const maxLabelSelectorLen = 1024

	if len(req.Msg.PageToken) > maxPageTokenLen {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("page_token exceeds maximum length of %d", maxPageTokenLen))
	}
	if len(req.Msg.LabelSelector) > maxLabelSelectorLen {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("label_selector exceeds maximum length of %d", maxLabelSelectorLen))
	}

	// RBAC check
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "list", namespace, "previewgroups"); err != nil {
		return nil, err
	}

	pageSize := int(req.Msg.PageSize)
	if pageSize <= 0 {
		pageSize = 100
	} else if pageSize > 1000 {
		pageSize = 1000
	}

	user, _ := auth.UserInfoFromContext(ctx)
	macaroonCaller := isMacaroonUser(user)
	needsPostFilter := namespace != "" || macaroonCaller

	var list v1alpha1.PreviewGroupList
	opts := []client.ListOption{}
	if !needsPostFilter {
		opts = append(opts, client.Limit(int64(pageSize)))
		if req.Msg.PageToken != "" {
			opts = append(opts, client.Continue(req.Msg.PageToken))
		}
	}

	if req.Msg.LabelSelector != "" {
		selector, err := labels.Parse(req.Msg.LabelSelector)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid label_selector: %w", err))
		}
		opts = append(opts, client.MatchingLabelsSelector{Selector: selector})
	}

	// PreviewGroup is cluster-scoped (metadata.namespace == "" on real K8s), so never pass client.InNamespace;
	// filter by previewGroupMatchesNamespace and Macaroon sandbox confinement after listing.
	if err := s.client.List(ctx, &list, opts...); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	var pbs []*pb.PreviewGroup
	for i := range list.Items {
		item := &list.Items[i]
		if namespace != "" && !previewGroupMatchesNamespace(item, namespace) {
			continue
		}
		if macaroonCaller && !previewGroupAllowedForMacaroon(item, namespace) {
			continue
		}
		dom, err := CRDPgToProto(item)
		if err != nil {
			s.logger.Warn("mapper error", "resource", item.Name, "error", err)
			continue
		}
		if dom != nil {
			pbs = append(pbs, dom)
		}
	}

	nextToken := list.Continue
	if needsPostFilter {
		slices.SortFunc(pbs, func(a, b *pb.PreviewGroup) int {
			return cmp.Compare(a.GetName(), b.GetName())
		})
		startIdx := 0
		if req.Msg.PageToken != "" {
			afterName, ok := strings.CutPrefix(req.Msg.PageToken, "after:")
			if !ok || strings.TrimSpace(afterName) == "" {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid page_token"))
			}
			startIdx = len(pbs)
			for i, item := range pbs {
				if item.GetName() > afterName {
					startIdx = i
					break
				}
			}
		}
		if startIdx >= len(pbs) {
			pbs = nil
			nextToken = ""
		} else {
			end := startIdx + pageSize
			if end < len(pbs) {
				nextToken = "after:" + pbs[end-1].GetName()
				pbs = pbs[startIdx:end]
			} else {
				nextToken = ""
				pbs = pbs[startIdx:]
			}
		}
	}

	return connect.NewResponse(&pb.ListPreviewGroupsResponse{
		PreviewGroups: pbs,
		NextPageToken: nextToken,
	}), nil
}

func (s *PreviewGroupService) UpdatePreviewGroup(ctx context.Context, req *connect.Request[pb.UpdatePreviewGroupRequest]) (*connect.Response[pb.UpdatePreviewGroupResponse], error) {
	msg := req.Msg
	if msg.PreviewGroup == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("preview group is required"))
	}

	if msg.PreviewGroup.ResourceVersion == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("resource_version is required; fetch the current resource first and include its resource_version to prevent concurrent modification"))
	}

	if msg.PreviewGroup.Name != "" {
		if err := ValidateDNS1123Label(msg.PreviewGroup.Name, "name"); err != nil {
			return nil, err
		}
	}

	lookupNS, rawAuthzNS, explicitNS, err := normalizePreviewGroupNamespace(msg.PreviewGroup.Namespace)
	if err != nil {
		return nil, err
	}

	authzNS, err := s.authorizePreLookup(ctx, "update", rawAuthzNS, explicitNS)
	if err != nil {
		return nil, err
	}

	var existingCrd v1alpha1.PreviewGroup
	if err := s.getPreviewGroupCRD(ctx, msg.PreviewGroup.Name, lookupNS, &existingCrd); err != nil {
		if apierrors.IsNotFound(err) {
			if fbErr := s.authorizeNotFoundFallback(ctx, "update", authzNS); fbErr != nil {
				return nil, fbErr
			}
		}
		return nil, SanitizeK8sError(s.logger, err)
	}
	priorPg := existingCrd.DeepCopy()

	if msg.PreviewGroup.ResourceVersion != "" {
		existingCrd.ResourceVersion = msg.PreviewGroup.ResourceVersion
	}

	newCrd, err := ProtoPgToCRD(msg.PreviewGroup)
	if err != nil {
		if strings.Contains(err.Error(), "invalid label") || strings.Contains(err.Error(), "invalid annotation") {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal server error"))
	}

	// Preserve CRD-only TopologyOverrides across ProtoPgToCRD round-trips so UI/MCP updates never drop them.
	if len(priorPg.Spec.TopologyOverrides) > 0 && len(newCrd.Spec.TopologyOverrides) == 0 {
		newCrd.Spec.TopologyOverrides = make([]v1alpha1.TopologyEdge, len(priorPg.Spec.TopologyOverrides))
		copy(newCrd.Spec.TopologyOverrides, priorPg.Spec.TopologyOverrides)
	}

	specTouched := true
	if msg.UpdateMask != nil && len(msg.UpdateMask.Paths) > 0 {
		specTouched = false
		allowedPaths := map[string]bool{"spec": true, "labels": true, "annotations": true}
		for _, path := range msg.UpdateMask.Paths {
			if !allowedPaths[path] {
				return nil, connect.NewError(connect.CodeInvalidArgument,
					fmt.Errorf("unsupported field mask path: %q; allowed paths are: spec, labels, annotations", path))
			}
			if path == "spec" {
				specTouched = true
			}
		}

		for _, path := range msg.UpdateMask.Paths {
			switch path {
			case "spec":
				existingCrd.Spec = newCrd.Spec
			case "labels":
				existingCrd.Labels = newCrd.Labels
			case "annotations":
				existingCrd.Annotations = newCrd.Annotations
			}
		}
	} else {
		existingCrd.Spec = newCrd.Spec
		existingCrd.Labels = newCrd.Labels
		existingCrd.Annotations = newCrd.Annotations
	}

	// Authorize per-namespace service additions, modifications, and removals against BOTH priorPg and existingCrd.
	if err := s.authorizeUpdateGroup(ctx, authzNS, priorPg, &existingCrd, specTouched); err != nil {
		return nil, err
	}

	if err := enforcePreviewGroupValidation(priorPg, &existingCrd); err != nil {
		return nil, err
	}

	if err := s.client.Update(ctx, &existingCrd); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	if msg.UpdateMask != nil && len(msg.UpdateMask.Paths) > 0 {
		s.logger.Info("partial update applied", "resource", "previewgroup", "name", existingCrd.Name, "paths", msg.UpdateMask.Paths)
	}

	s.auditLogger.LogMutation(ctx, "resource.updated", "previewgroup", existingCrd.Name, existingCrd.Namespace)

	domBack, err := CRDPgToProto(&existingCrd)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal server error"))
	}
	if domBack == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal server error"))
	}
	return connect.NewResponse(&pb.UpdatePreviewGroupResponse{
		PreviewGroup: domBack,
	}), nil
}

func (s *PreviewGroupService) DeletePreviewGroup(ctx context.Context, req *connect.Request[pb.DeletePreviewGroupRequest]) (*connect.Response[pb.DeletePreviewGroupResponse], error) {
	if err := ValidateDNS1123Label(req.Msg.Name, "name"); err != nil {
		return nil, err
	}
	lookupNS, rawAuthzNS, explicitNS, err := normalizePreviewGroupNamespace(req.Msg.Namespace)
	if err != nil {
		return nil, err
	}

	authzNS, err := s.authorizePreLookup(ctx, "delete", rawAuthzNS, explicitNS)
	if err != nil {
		return nil, err
	}

	var crd v1alpha1.PreviewGroup
	if err := s.getPreviewGroupCRD(ctx, req.Msg.Name, lookupNS, &crd); err != nil {
		if apierrors.IsNotFound(err) {
			if fbErr := s.authorizeNotFoundFallback(ctx, "delete", authzNS); fbErr != nil {
				return nil, fbErr
			}
		}
		return nil, SanitizeK8sError(s.logger, err)
	}

	// Require delete permission across ALL namespaces where this PreviewGroup currently manages services.
	if err := s.authorizeDeleteGroup(ctx, authzNS, &crd); err != nil {
		return nil, err
	}

	// Optional optimistic concurrency guard via If-Match header (prevents final-service detach from deleting a concurrent join).
	if ifMatchRV := strings.TrimSpace(req.Header().Get(IfMatchHeader)); ifMatchRV != "" && crd.ResourceVersion != "" && crd.ResourceVersion != ifMatchRV {
		return nil, connect.NewError(
			connect.CodeAborted,
			fmt.Errorf("preview group %q was modified concurrently; refresh and retry", crd.Name),
		)
	}

	var delOpts []client.DeleteOption
	if crd.UID != "" || crd.ResourceVersion != "" {
		precond := &metav1.Preconditions{}
		if crd.UID != "" {
			uid := crd.UID
			precond.UID = &uid
		}
		if crd.ResourceVersion != "" {
			rv := crd.ResourceVersion
			precond.ResourceVersion = &rv
		}
		delOpts = append(delOpts, &client.DeleteOptions{Preconditions: precond})
	}

	if err := s.client.Delete(ctx, &crd, delOpts...); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	s.auditLogger.LogMutation(ctx, "resource.deleted", "previewgroup", req.Msg.Name, crd.Namespace)

	return connect.NewResponse(&pb.DeletePreviewGroupResponse{}), nil
}

func (s *PreviewGroupService) WatchPreviewGroups(ctx context.Context, req *connect.Request[pb.WatchPreviewGroupsRequest], stream *connect.ServerStream[pb.WatchPreviewGroupsResponse]) error {
	if s.informerMgr == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("informer manager is not configured"))
	}

	release, err := s.limiter.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	namespace := req.Msg.Namespace
	if namespace != "" {
		if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
			return err
		}
	}

	// RBAC check — required even for cluster-wide watches
	if namespace != "" {
		if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "watch", namespace, "previewgroups"); err != nil {
			return err
		}
	} else {
		// Cluster-wide watch: check watch permission at cluster scope (empty namespace)
		if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "watch", "", "previewgroups"); err != nil {
			return err
		}
	}

	user, _ := auth.UserInfoFromContext(ctx)
	macaroonCaller := isMacaroonUser(user)

	// Subscribe FIRST to prevent race condition (Subscribe → List → deduplicate)
	sub, err := s.informerMgr.PgBroadcaster.Subscribe(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("server is shutting down"))
	}
	defer s.informerMgr.PgBroadcaster.Unsubscribe(sub.ID())

	// List current state (PreviewGroup is cluster-scoped, so never pass client.InNamespace)
	var list v1alpha1.PreviewGroupList
	if err := s.client.List(ctx, &list); err != nil {
		return SanitizeK8sError(s.logger, err)
	}

	listedRVs := make(map[string]uint64, len(list.Items))
	lastSentRV := make(map[string]string, len(list.Items))
	lastDeletedRV := make(map[string]string)
	emittedLast := make(map[string]*pb.PreviewGroup, len(list.Items))

	recordDeletedRV := func(objKey, rv string) {
		if rv == "" {
			return
		}
		if len(lastDeletedRV) >= 1024 {
			clear(lastDeletedRV)
		}
		lastDeletedRV[objKey] = rv
	}

	for i := range list.Items {
		crd := &list.Items[i]
		objKey := crd.Namespace + "/" + crd.Name
		if rvNum, ok := parseDecimalRV(crd.ResourceVersion); ok {
			listedRVs[objKey] = rvNum
		}
		if namespace != "" && !previewGroupMatchesNamespace(crd, namespace) {
			continue
		}
		if macaroonCaller && !previewGroupAllowedForMacaroon(crd, namespace) {
			continue
		}
		dom, err := CRDPgToProto(crd)
		if err != nil {
			s.logger.Warn("mapper error", "resource", crd.Name, "error", err)
			continue
		}
		if dom != nil {
			emittedLast[objKey] = dom
			if crd.ResourceVersion != "" {
				lastSentRV[objKey] = crd.ResourceVersion
			}
			if err := stream.Send(&pb.WatchPreviewGroupsResponse{
				Type:            pb.WatchEventType_WATCH_EVENT_TYPE_ADDED,
				PreviewGroup:    dom,
				ResourceVersion: crd.ResourceVersion,
			}); err != nil {
				return err
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-sub.Events():
			if !ok {
				if s.informerMgr.PgBroadcaster.IsClosed() {
					return connect.NewError(connect.CodeUnavailable, errors.New("server shutting down"))
				}
				return connect.NewError(connect.CodeResourceExhausted, errors.New("event buffer overflow, please reconnect"))
			}
			if event.Object == nil {
				continue
			}

			objKey := event.Object.Namespace + "/" + event.Object.Name
			evRV, hasEvRV := parseDecimalRV(event.Version)
			listedRV, inInitialList := listedRVs[objKey]
			matches := (namespace == "" || previewGroupMatchesNamespace(event.Object, namespace)) &&
				(!macaroonCaller || previewGroupAllowedForMacaroon(event.Object, namespace))

			if event.Type == "DELETED" {
				if inInitialList && hasEvRV && evRV < listedRV {
					continue
				}
				if event.Version != "" && lastDeletedRV[objKey] == event.Version {
					continue
				}
				prevDom, wasEmitted := emittedLast[objKey]
				if !wasEmitted && !matches {
					continue
				}
				delete(emittedLast, objKey)
				delete(lastSentRV, objKey)
				delete(listedRVs, objKey)
				payload := prevDom
				if matches {
					if dom, mErr := CRDPgToProto(event.Object); mErr == nil && dom != nil {
						payload = dom
					}
				}
				if payload == nil {
					continue
				}
				recordDeletedRV(objKey, event.Version)
				if err := stream.Send(&pb.WatchPreviewGroupsResponse{
					Type:            pb.WatchEventType_WATCH_EVENT_TYPE_DELETED,
					PreviewGroup:    payload,
					ResourceVersion: event.Version,
				}); err != nil {
					return err
				}
				continue
			}

			if inInitialList && hasEvRV {
				if evRV <= listedRV {
					continue
				}
				delete(listedRVs, objKey)
			}
			if event.Version != "" && lastSentRV[objKey] == event.Version {
				continue
			}

			if !matches {
				// Group previously visible on this stream moved out of the watched/allowed namespace:
				// emit a DELETED event carrying ONLY the previously visible snapshot.
				if prevDom, wasEmitted := emittedLast[objKey]; wasEmitted {
					delete(emittedLast, objKey)
					delete(lastSentRV, objKey)
					recordDeletedRV(objKey, event.Version)
					if err := stream.Send(&pb.WatchPreviewGroupsResponse{
						Type:            pb.WatchEventType_WATCH_EVENT_TYPE_DELETED,
						PreviewGroup:    prevDom,
						ResourceVersion: event.Version,
					}); err != nil {
						return err
					}
				}
				continue
			}

			dom, err := CRDPgToProto(event.Object)
			if err != nil {
				s.logger.Warn("mapper error", "resource", event.Object.Name, "error", err)
				continue
			}
			if dom == nil {
				continue
			}

			_, wasEmitted := emittedLast[objKey]
			delete(lastDeletedRV, objKey)
			emittedLast[objKey] = dom
			if event.Version != "" {
				lastSentRV[objKey] = event.Version
			}

			var eventType pb.WatchEventType
			switch event.Type {
			case "ADDED":
				eventType = pb.WatchEventType_WATCH_EVENT_TYPE_ADDED
			case "MODIFIED":
				if !wasEmitted {
					eventType = pb.WatchEventType_WATCH_EVENT_TYPE_ADDED
				} else {
					eventType = pb.WatchEventType_WATCH_EVENT_TYPE_MODIFIED
				}
			default:
				eventType = pb.WatchEventType_WATCH_EVENT_TYPE_UNSPECIFIED
			}

			if err := stream.Send(&pb.WatchPreviewGroupsResponse{
				Type:            eventType,
				PreviewGroup:    dom,
				ResourceVersion: event.Version,
			}); err != nil {
				return err
			}
		}
	}
}
