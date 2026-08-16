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

type PreviewGroupService struct {
	client client.Client
}

func NewPreviewGroupService(c client.Client) divergev1alpha1connect.PreviewGroupServiceHandler {
	return &PreviewGroupService{client: c}
}

func (s *PreviewGroupService) authorizeAction(ctx context.Context, namespace, name, verb string) error {
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
				Resource:  "previewgroups",
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

func (s *PreviewGroupService) CreatePreviewGroup(ctx context.Context, req *connect.Request[pb.CreatePreviewGroupRequest]) (*connect.Response[pb.CreatePreviewGroupResponse], error) {
	msg := req.Msg
	if msg.Namespace == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("namespace is required"))
	}
	if msg.PreviewGroup == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("preview group is required"))
	}
	if msg.PreviewGroup.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("preview group name is required"))
	}

	if err := s.authorizeAction(ctx, msg.Namespace, msg.PreviewGroup.Name, "create"); err != nil {
		return nil, err
	}

	realCrd := ProtoToPreviewGroup(msg.PreviewGroup)
	realCrd.Namespace = msg.Namespace

	if err := s.client.Create(ctx, realCrd); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&pb.CreatePreviewGroupResponse{
		PreviewGroup: PreviewGroupToProto(realCrd),
	}), nil
}

func (s *PreviewGroupService) GetPreviewGroup(ctx context.Context, req *connect.Request[pb.GetPreviewGroupRequest]) (*connect.Response[pb.GetPreviewGroupResponse], error) {
	if req.Msg.Namespace == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("namespace is required"))
	}
	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}

	if err := s.authorizeAction(ctx, req.Msg.Namespace, req.Msg.Name, "get"); err != nil {
		return nil, err
	}

	var crd v1alpha1.PreviewGroup
	err := s.client.Get(ctx, types.NamespacedName{Name: req.Msg.Name, Namespace: req.Msg.Namespace}, &crd)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&pb.GetPreviewGroupResponse{
		PreviewGroup: PreviewGroupToProto(&crd),
	}), nil
}

func (s *PreviewGroupService) ListPreviewGroups(ctx context.Context, req *connect.Request[pb.ListPreviewGroupsRequest]) (*connect.Response[pb.ListPreviewGroupsResponse], error) {
	if req.Msg.Namespace == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("namespace is required"))
	}

	if err := s.authorizeAction(ctx, req.Msg.Namespace, "", "list"); err != nil {
		return nil, err
	}

	var list v1alpha1.PreviewGroupList
	if err := s.client.List(ctx, &list, client.InNamespace(req.Msg.Namespace)); err != nil {
		return nil, toConnectError(err)
	}

	var pbs []*pb.PreviewGroup
	for i := range list.Items {
		pbPg := PreviewGroupToProto(&list.Items[i])
		if pbPg != nil {
			pbs = append(pbs, pbPg)
		}
	}

	return connect.NewResponse(&pb.ListPreviewGroupsResponse{
		PreviewGroups: pbs,
	}), nil
}

func (s *PreviewGroupService) DeletePreviewGroup(ctx context.Context, req *connect.Request[pb.DeletePreviewGroupRequest]) (*connect.Response[pb.DeletePreviewGroupResponse], error) {
	if req.Msg.Namespace == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("namespace is required"))
	}
	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}

	if err := s.authorizeAction(ctx, req.Msg.Namespace, req.Msg.Name, "delete"); err != nil {
		return nil, err
	}

	var crd v1alpha1.PreviewGroup
	crd.Name = req.Msg.Name
	crd.Namespace = req.Msg.Namespace
	if err := s.client.Delete(ctx, &crd); err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&pb.DeletePreviewGroupResponse{}), nil
}

func (s *PreviewGroupService) WatchPreviewGroups(ctx context.Context, req *connect.Request[pb.WatchPreviewGroupsRequest], stream *connect.ServerStream[pb.WatchPreviewGroupsResponse]) error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("unimplemented"))
}
