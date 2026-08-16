package server

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	authzv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/api/v1alpha1"
)

type EnvironmentService struct {
	client client.Client
}

func NewEnvironmentService(c client.Client) divergev1alpha1connect.EnvironmentServiceHandler {
	return &EnvironmentService{client: c}
}

func (s *EnvironmentService) authorizeAction(ctx context.Context, namespace, name, verb string) error {
	u, ok := UserFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}

	sar := &authzv1.SubjectAccessReview{
		Spec: authzv1.SubjectAccessReviewSpec{
			User:   u.ID,
			Groups: u.Groups,
			ResourceAttributes: &authzv1.ResourceAttributes{
				Group:     "diverge.dev",
				Resource:  "environments",
				Verb:      verb,
				Namespace: namespace,
				Name:      name,
			},
		},
	}

	if err := s.client.Create(ctx, sar); err != nil {
		return toConnectError(err)
	}
	if !sar.Status.Allowed {
		return connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}
	return nil
}

func (s *EnvironmentService) CreateEnvironment(ctx context.Context, req *connect.Request[pb.CreateEnvironmentRequest]) (*connect.Response[pb.CreateEnvironmentResponse], error) {
	msg := req.Msg
	if msg.Namespace == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("namespace is required"))
	}
	if msg.Environment == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("environment is required"))
	}
	if msg.Environment.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("environment name is required"))
	}

	if err := s.authorizeAction(ctx, msg.Namespace, msg.Environment.Name, "create"); err != nil {
		return nil, err
	}

	realCrd := ProtoToEnvironment(msg.Environment)
	realCrd.Namespace = msg.Namespace

	if err := s.client.Create(ctx, realCrd); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&pb.CreateEnvironmentResponse{
		Environment: EnvironmentToProto(realCrd),
	}), nil
}

func (s *EnvironmentService) GetEnvironment(ctx context.Context, req *connect.Request[pb.GetEnvironmentRequest]) (*connect.Response[pb.GetEnvironmentResponse], error) {
	if req.Msg.Namespace == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("namespace is required"))
	}
	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}

	if err := s.authorizeAction(ctx, req.Msg.Namespace, req.Msg.Name, "get"); err != nil {
		return nil, err
	}

	var crd v1alpha1.Environment
	err := s.client.Get(ctx, types.NamespacedName{Name: req.Msg.Name, Namespace: req.Msg.Namespace}, &crd)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&pb.GetEnvironmentResponse{
		Environment: EnvironmentToProto(&crd),
	}), nil
}

func (s *EnvironmentService) ListEnvironments(ctx context.Context, req *connect.Request[pb.ListEnvironmentsRequest]) (*connect.Response[pb.ListEnvironmentsResponse], error) {
	if req.Msg.Namespace == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("namespace is required"))
	}

	if err := s.authorizeAction(ctx, req.Msg.Namespace, "", "list"); err != nil {
		return nil, err
	}

	var list v1alpha1.EnvironmentList
	if err := s.client.List(ctx, &list, client.InNamespace(req.Msg.Namespace)); err != nil {
		return nil, toConnectError(err)
	}

	var pbs []*pb.Environment
	for i := range list.Items {
		pbEnv := EnvironmentToProto(&list.Items[i])
		if pbEnv != nil {
			pbs = append(pbs, pbEnv)
		}
	}

	return connect.NewResponse(&pb.ListEnvironmentsResponse{
		Environments: pbs,
	}), nil
}

func (s *EnvironmentService) UpdateEnvironment(ctx context.Context, req *connect.Request[pb.UpdateEnvironmentRequest]) (*connect.Response[pb.UpdateEnvironmentResponse], error) {
	msg := req.Msg
	if msg.Environment == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("environment is required"))
	}
	if msg.Environment.Namespace == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("namespace is required"))
	}
	if msg.Environment.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("environment name is required"))
	}

	if err := s.authorizeAction(ctx, msg.Environment.Namespace, msg.Environment.Name, "update"); err != nil {
		return nil, err
	}

	realCrd := ProtoToEnvironment(msg.Environment)

	if err := s.client.Update(ctx, realCrd); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&pb.UpdateEnvironmentResponse{
		Environment: EnvironmentToProto(realCrd),
	}), nil
}

func (s *EnvironmentService) DeleteEnvironment(ctx context.Context, req *connect.Request[pb.DeleteEnvironmentRequest]) (*connect.Response[pb.DeleteEnvironmentResponse], error) {
	if req.Msg.Namespace == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("namespace is required"))
	}
	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}

	if err := s.authorizeAction(ctx, req.Msg.Namespace, req.Msg.Name, "delete"); err != nil {
		return nil, err
	}

	var crd v1alpha1.Environment
	crd.Name = req.Msg.Name
	crd.Namespace = req.Msg.Namespace
	if err := s.client.Delete(ctx, &crd); err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&pb.DeleteEnvironmentResponse{}), nil
}

func (s *EnvironmentService) ExtendTTL(ctx context.Context, req *connect.Request[pb.ExtendTTLRequest]) (*connect.Response[pb.ExtendTTLResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("unimplemented"))
}

func (s *EnvironmentService) WatchEnvironments(ctx context.Context, req *connect.Request[pb.WatchEnvironmentsRequest], stream *connect.ServerStream[pb.WatchEnvironmentsResponse]) error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("unimplemented"))
}

func (s *EnvironmentService) StreamLogs(ctx context.Context, req *connect.Request[pb.StreamLogsRequest], stream *connect.ServerStream[pb.StreamLogsResponse]) error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("unimplemented"))
}
