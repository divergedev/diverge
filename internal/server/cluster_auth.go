package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server/auth"
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

	return connect.NewResponse(&pb.GetClusterInfoResponse{
		ControllerVersion: s.version,
		ControllerHealthy: true,
		EnvironmentCount:  int32(len(envList.Items)),
		PreviewGroupCount: int32(len(pgList.Items)),
	}), nil
}

type AuthService struct {
	k8sClient   kubernetes.Interface
	logger      *slog.Logger
	auditLogger *AuditLogger
}

func NewAuthService(k8s kubernetes.Interface, logger *slog.Logger, auditLogger *AuditLogger) divergev1alpha1connect.AuthServiceHandler {
	return &AuthService{k8sClient: k8s, logger: logger, auditLogger: auditLogger}
}

func (s *AuthService) GetCurrentUser(ctx context.Context, req *connect.Request[pb.GetCurrentUserRequest]) (*connect.Response[pb.GetCurrentUserResponse], error) {
	user, ok := auth.UserInfoFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	issuer := "kubernetes"
	if user.Email != "" {
		issuer = "oidc" // OIDC-authenticated users have email populated
	}

	return connect.NewResponse(&pb.GetCurrentUserResponse{
		UserId: user.Username,
		Email:  user.Email,
		Groups: user.Groups,
		Issuer: issuer,
	}), nil
}

func (s *AuthService) ListPermissions(ctx context.Context, req *connect.Request[pb.ListPermissionsRequest]) (*connect.Response[pb.ListPermissionsResponse], error) {
	if _, ok := auth.UserInfoFromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	// Check actual permissions for Diverge resources
	resources := []string{"environments", "previewgroups"}
	verbs := []string{"get", "list", "watch", "create", "update", "delete"}
	namespace := req.Msg.Namespace
	if namespace == "" {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}

	// Single operation-level deadline for the entire permission scan
	const permScanTimeout = 30 * time.Second
	scanCtx, scanCancel := context.WithTimeout(ctx, permScanTimeout)
	defer scanCancel()

	var permissions []*pb.Permission
	for _, resource := range resources {
		var allowedVerbs []string
		for _, verb := range verbs {
			err := AuthorizeAction(scanCtx, s.k8sClient, s.auditLogger, verb, namespace, resource)

			if err == nil {
				allowedVerbs = append(allowedVerbs, verb)
			} else {
				// Distinguish permission denied (expected) from actual errors
				var connectErr *connect.Error
				if errors.As(err, &connectErr) && connectErr.Code() == connect.CodePermissionDenied {
					// Not allowed — expected, continue
					continue
				}
				// Actual error — log and propagate
				s.logger.Error("permission check failed",
					"resource", resource,
					"verb", verb,
					"namespace", namespace,
					"error", err,
				)
				return nil, connect.NewError(connect.CodeInternal, errors.New("permission check failed"))
			}
		}
		if len(allowedVerbs) > 0 {
			permissions = append(permissions, &pb.Permission{
				Resource:   resource,
				Verbs:      allowedVerbs,
				Namespaces: []string{namespace},
			})
		}
	}

	return connect.NewResponse(&pb.ListPermissionsResponse{
		Permissions: permissions,
	}), nil
}
