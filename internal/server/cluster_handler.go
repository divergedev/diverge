package server

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/api/v1alpha1"
)

const (
	annotationMaxActiveEnvironments = "divergedev.com/max-active-environments"
	annotationMaxCNPGClusters       = "divergedev.com/max-cnpg-clusters"
	annotationMaxHourlyCostUSD      = "divergedev.com/max-hourly-cost-usd"
	annotationQuotaExceedPolicy     = "divergedev.com/quota-exceed-policy"
	quotaPolicyHibernateOldest      = "hibernate-oldest"
	quotaPolicyReject               = "reject"
)

type ClusterService struct {
	client      client.Client
	k8sClient   kubernetes.Interface
	logger      *slog.Logger
	auditLogger *AuditLogger
	version     string
}

// NewClusterService creates a ClusterService with the given version string.
// Pass the build-injected version (e.g. from ldflags) for accurate reporting.
func NewClusterService(c client.Client, k8s kubernetes.Interface, logger *slog.Logger, auditLogger *AuditLogger, version string) divergev1alpha1connect.ClusterServiceHandler {
	if version == "" {
		version = "dev"
	}
	return &ClusterService{client: c, k8sClient: k8s, logger: logger, auditLogger: auditLogger, version: version}
}

// GetClusterInfo returns cluster-wide summary counts, controller health status, and namespace quota configuration.
func (s *ClusterService) GetClusterInfo(ctx context.Context, req *connect.Request[pb.GetClusterInfoRequest]) (*connect.Response[pb.GetClusterInfoResponse], error) {
	const apiTimeout = 5 * time.Second

	// RBAC check: cluster-scoped read of environments
	sarCtx, sarCancel := context.WithTimeout(ctx, apiTimeout)
	err := AuthorizeAction(sarCtx, s.k8sClient, s.auditLogger, "list", "", "environments")
	sarCancel()
	if err != nil {
		return nil, err
	}

	// RBAC check: cluster-scoped read of previewgroups
	sarCtx2, sarCancel2 := context.WithTimeout(ctx, apiTimeout)
	err = AuthorizeAction(sarCtx2, s.k8sClient, s.auditLogger, "list", "", "previewgroups")
	sarCancel2()
	if err != nil {
		return nil, err
	}

	// Count environments
	listCtx, listCancel := context.WithTimeout(ctx, apiTimeout)
	defer listCancel()
	var envList v1alpha1.EnvironmentList
	if err := s.client.List(listCtx, &envList); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	// Count preview groups
	listCtx2, listCancel2 := context.WithTimeout(ctx, apiTimeout)
	defer listCancel2()
	var pgList v1alpha1.PreviewGroupList
	if err := s.client.List(listCtx2, &pgList); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	resp := &pb.GetClusterInfoResponse{
		ControllerVersion: s.version,
		ControllerHealthy: true,
		EnvironmentCount:  int32(len(envList.Items)),
		PreviewGroupCount: int32(len(pgList.Items)),
		NamespaceQuota:    s.getNamespaceQuota(ctx, "default", apiTimeout),
	}

	return connect.NewResponse(resp), nil
}

func (s *ClusterService) getNamespaceQuota(ctx context.Context, namespace string, timeout time.Duration) *pb.NamespaceQuotaInfo {
	if s == nil {
		return nil
	}
	if strings.TrimSpace(namespace) == "" {
		namespace = "default"
	}

	var annotations map[string]string
	if s.k8sClient != nil {
		nsCtx, cancel := context.WithTimeout(ctx, timeout)
		ns, err := s.k8sClient.CoreV1().Namespaces().Get(nsCtx, namespace, metav1.GetOptions{})
		cancel()
		if err == nil && ns != nil {
			annotations = ns.Annotations
		}
	}
	if len(annotations) == 0 && s.client != nil {
		nsCtx, cancel := context.WithTimeout(ctx, timeout)
		var ns corev1.Namespace
		err := s.client.Get(nsCtx, types.NamespacedName{Name: namespace}, &ns)
		cancel()
		if err == nil {
			annotations = ns.Annotations
		}
	}
	if len(annotations) == 0 {
		return nil
	}

	var maxActive int32
	if raw := strings.TrimSpace(annotations[annotationMaxActiveEnvironments]); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			maxActive = int32(v)
		}
	}

	var maxCNPG int32
	if raw := strings.TrimSpace(annotations[annotationMaxCNPGClusters]); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			maxCNPG = int32(v)
		}
	}

	var maxHourlyCost float64
	if raw := strings.TrimPrefix(strings.TrimSpace(annotations[annotationMaxHourlyCostUSD]), "$"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v > 0 {
			maxHourlyCost = v
		}
	}

	rawPolicy := strings.ToLower(strings.TrimSpace(annotations[annotationQuotaExceedPolicy]))
	if maxActive <= 0 && maxCNPG <= 0 && maxHourlyCost <= 0 && rawPolicy == "" {
		return nil
	}

	policy := quotaPolicyHibernateOldest
	if rawPolicy == quotaPolicyReject {
		policy = quotaPolicyReject
	} else if rawPolicy != "" {
		policy = rawPolicy
	}

	return &pb.NamespaceQuotaInfo{
		MaxActiveEnvironments: maxActive,
		MaxCnpgClusters:       maxCNPG,
		MaxHourlyCostUsd:      maxHourlyCost,
		QuotaExceedPolicy:     policy,
	}
}
