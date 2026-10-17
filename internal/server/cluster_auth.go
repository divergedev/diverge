package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"k8s.io/client-go/kubernetes"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/internal/server/auth"
)

type AuthService struct {
	k8sClient   kubernetes.Interface
	logger      *slog.Logger
	auditLogger *AuditLogger
}

// NewAuthService creates an AuthService backed by the given Kubernetes client and loggers.
func NewAuthService(k8s kubernetes.Interface, logger *slog.Logger, auditLogger *AuditLogger) divergev1alpha1connect.AuthServiceHandler {
	return &AuthService{k8sClient: k8s, logger: logger, auditLogger: auditLogger}
}

// GetCurrentUser returns the authenticated user's identity and issuer type.
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

// ListPermissions evaluates and returns the authenticated user's permitted verbs on Diverge resources in the requested namespace.
func (s *AuthService) ListPermissions(ctx context.Context, req *connect.Request[pb.ListPermissionsRequest]) (*connect.Response[pb.ListPermissionsResponse], error) {
	if _, ok := auth.UserInfoFromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	// Check actual permissions for Diverge resources
	resources := []string{"environments", "previewgroups", "agenttasks"}
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
