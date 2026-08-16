package server

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/api/v1alpha1"
	domain "github.com/divergedev/diverge/gen/domain/github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
)

type EnvironmentService struct {
	client client.Client
}

func NewEnvironmentService(c client.Client) divergev1alpha1connect.EnvironmentServiceHandler {
	return &EnvironmentService{client: c}
}

func (s *EnvironmentService) CreateEnvironment(ctx context.Context, req *connect.Request[pb.CreateEnvironmentRequest]) (*connect.Response[pb.CreateEnvironmentResponse], error) {
	msg := req.Msg
	if msg.Environment == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("environment is required"))
	}

	var dom domain.Environment
	dom.FromProto(msg.Environment)

	// empty replacement

	realCrd, err := DomainEnvToCRD(&dom)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	realCrd.Namespace = msg.Namespace
	if realCrd.Namespace == "" {
		realCrd.Namespace = "default"
	}

	if err := s.client.Create(ctx, realCrd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Read back to return
	var back domain.Environment
	domBack, _ := CRDEnvToDomain(realCrd)
	if domBack != nil {
		back = *domBack
	}
	return connect.NewResponse(&pb.CreateEnvironmentResponse{
		Environment: back.ToProto(),
	}), nil
}

func (s *EnvironmentService) GetEnvironment(ctx context.Context, req *connect.Request[pb.GetEnvironmentRequest]) (*connect.Response[pb.GetEnvironmentResponse], error) {
	var crd v1alpha1.Environment
	err := s.client.Get(ctx, types.NamespacedName{Name: req.Msg.Name, Namespace: req.Msg.Namespace}, &crd)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	dom, err := CRDEnvToDomain(&crd)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.GetEnvironmentResponse{
		Environment: dom.ToProto(),
	}), nil
}

func (s *EnvironmentService) ListEnvironments(ctx context.Context, req *connect.Request[pb.ListEnvironmentsRequest]) (*connect.Response[pb.ListEnvironmentsResponse], error) {
	var list v1alpha1.EnvironmentList
	if err := s.client.List(ctx, &list, client.InNamespace(req.Msg.Namespace)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var pbs []*pb.Environment
	for i := range list.Items {
		dom, _ := CRDEnvToDomain(&list.Items[i])
		if dom != nil {
			pbs = append(pbs, dom.ToProto())
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

	var dom domain.Environment
	dom.FromProto(msg.Environment)

	realCrd, err := DomainEnvToCRD(&dom)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := s.client.Update(ctx, realCrd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	domBack, _ := CRDEnvToDomain(realCrd)
	return connect.NewResponse(&pb.UpdateEnvironmentResponse{
		Environment: domBack.ToProto(),
	}), nil
}

func (s *EnvironmentService) DeleteEnvironment(ctx context.Context, req *connect.Request[pb.DeleteEnvironmentRequest]) (*connect.Response[pb.DeleteEnvironmentResponse], error) {
	var crd v1alpha1.Environment
	crd.Name = req.Msg.Name
	crd.Namespace = req.Msg.Namespace
	if err := s.client.Delete(ctx, &crd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
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
