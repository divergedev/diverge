package server

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/internal/finops"
)

// GetCostSummary computes per-namespace, per-service, and per-PR usage and cost attribution
// across preview environments, including hibernation savings, delta-routing savings, AgentTask LLM spend,
// optional OpenCost live allocation enrichment, and quota/TTL budget warnings.
func (s *ClusterService) GetCostSummary(ctx context.Context, req *connect.Request[pb.GetCostSummaryRequest]) (*connect.Response[pb.GetCostSummaryResponse], error) {
	const apiTimeout = 5 * time.Second

	reqNS := ""
	if req != nil && req.Msg != nil {
		reqNS = strings.TrimSpace(req.Msg.GetNamespace())
	}
	if reqNS != "" {
		if err := ValidateDNS1123Label(reqNS, "namespace"); err != nil {
			return nil, err
		}
	}

	sarCtx, sarCancel := context.WithTimeout(ctx, apiTimeout)
	err := AuthorizeCostSummary(sarCtx, s.k8sClient, s.auditLogger, reqNS)
	sarCancel()
	if err != nil {
		return nil, err
	}

	resp, err := finops.BuildCostSummary(ctx, finops.SummaryOptions{
		Namespace: reqNS,
		Client:    s.client,
		K8sClient: s.k8sClient,
		Timeout:   apiTimeout,
		CanListPreviewGroups: func(authCtx context.Context) bool {
			return AuthorizeAction(authCtx, s.k8sClient, nil, "list", reqNS, "previewgroups") == nil
		},
		CanListAgentTasks: func(authCtx context.Context) bool {
			return AuthorizeAction(authCtx, s.k8sClient, nil, "list", reqNS, "agenttasks") == nil
		},
	})
	if err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}
	return connect.NewResponse(resp), nil
}
