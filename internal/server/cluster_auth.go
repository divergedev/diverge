package server

import (
	"context"

	"connectrpc.com/connect"
	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
)

type ClusterService struct{}

func NewClusterService() divergev1alpha1connect.ClusterServiceHandler {
	return &ClusterService{}
}

func (s *ClusterService) GetClusterInfo(ctx context.Context, req *connect.Request[pb.GetClusterInfoRequest]) (*connect.Response[pb.GetClusterInfoResponse], error) {
	return connect.NewResponse(&pb.GetClusterInfoResponse{
		ControllerVersion: "v0.1.0",
		ControllerHealthy: true,
		EnvironmentCount:  0,
		PreviewGroupCount: 0,
		Namespaces:        []string{"default"},
	}), nil
}

type AuthService struct{}

func NewAuthService() divergev1alpha1connect.AuthServiceHandler {
	return &AuthService{}
}

func (s *AuthService) GetCurrentUser(ctx context.Context, req *connect.Request[pb.GetCurrentUserRequest]) (*connect.Response[pb.GetCurrentUserResponse], error) {
	return connect.NewResponse(&pb.GetCurrentUserResponse{
		UserId: "admin",
		Email:  "admin@diverge.dev",
		Groups: []string{"system:masters"},
		Issuer: "diverge-auth",
	}), nil
}

func (s *AuthService) ListPermissions(ctx context.Context, req *connect.Request[pb.ListPermissionsRequest]) (*connect.Response[pb.ListPermissionsResponse], error) {
	return connect.NewResponse(&pb.ListPermissionsResponse{
		Permissions: []*pb.Permission{
			{
				Resource:   "*",
				Verbs:      []string{"*"},
				Namespaces: []string{"*"},
			},
		},
	}), nil
}
