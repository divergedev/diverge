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
	"github.com/divergedev/diverge/internal/server/streaming"
)

type PreviewGroupService struct {
	client      client.Client
	informerMgr *streaming.InformerManager
}

func NewPreviewGroupService(c client.Client, informerMgr *streaming.InformerManager) divergev1alpha1connect.PreviewGroupServiceHandler {
	return &PreviewGroupService{
		client:      c,
		informerMgr: informerMgr,
	}
}

func (s *PreviewGroupService) CreatePreviewGroup(ctx context.Context, req *connect.Request[pb.CreatePreviewGroupRequest]) (*connect.Response[pb.CreatePreviewGroupResponse], error) {
	msg := req.Msg
	if msg.PreviewGroup == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("preview group is required"))
	}

	var dom domain.PreviewGroup
	dom.FromProto(msg.PreviewGroup)

	realCrd, err := DomainPgToCRD(&dom)
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

	var back domain.PreviewGroup
	domBack, _ := CRDPgToDomain(realCrd)
	if domBack != nil {
		back = *domBack
	}
	return connect.NewResponse(&pb.CreatePreviewGroupResponse{
		PreviewGroup: back.ToProto(),
	}), nil
}

func (s *PreviewGroupService) GetPreviewGroup(ctx context.Context, req *connect.Request[pb.GetPreviewGroupRequest]) (*connect.Response[pb.GetPreviewGroupResponse], error) {
	var crd v1alpha1.PreviewGroup
	err := s.client.Get(ctx, types.NamespacedName{Name: req.Msg.Name, Namespace: req.Msg.Namespace}, &crd)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	dom, err := CRDPgToDomain(&crd)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.GetPreviewGroupResponse{
		PreviewGroup: dom.ToProto(),
	}), nil
}

func (s *PreviewGroupService) ListPreviewGroups(ctx context.Context, req *connect.Request[pb.ListPreviewGroupsRequest]) (*connect.Response[pb.ListPreviewGroupsResponse], error) {
	var list v1alpha1.PreviewGroupList
	if err := s.client.List(ctx, &list, client.InNamespace(req.Msg.Namespace)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var pbs []*pb.PreviewGroup
	for i := range list.Items {
		dom, _ := CRDPgToDomain(&list.Items[i])
		if dom != nil {
			pbs = append(pbs, dom.ToProto())
		}
	}

	return connect.NewResponse(&pb.ListPreviewGroupsResponse{
		PreviewGroups: pbs,
	}), nil
}

func (s *PreviewGroupService) DeletePreviewGroup(ctx context.Context, req *connect.Request[pb.DeletePreviewGroupRequest]) (*connect.Response[pb.DeletePreviewGroupResponse], error) {
	var crd v1alpha1.PreviewGroup
	crd.Name = req.Msg.Name
	crd.Namespace = req.Msg.Namespace
	if err := s.client.Delete(ctx, &crd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.DeletePreviewGroupResponse{}), nil
}

func (s *PreviewGroupService) WatchPreviewGroups(ctx context.Context, req *connect.Request[pb.WatchPreviewGroupsRequest], stream *connect.ServerStream[pb.WatchPreviewGroupsResponse]) error {
	if s.informerMgr == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("informer manager is not configured"))
	}

	namespace := req.Msg.Namespace

	sub := s.informerMgr.PgBroadcaster.Subscribe(ctx)

	// List initial state
	var list v1alpha1.PreviewGroupList
	if err := s.client.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	for i := range list.Items {
		crd := &list.Items[i]
		dom, _ := CRDPgToDomain(crd)
		if dom != nil {
			if err := stream.Send(&pb.WatchPreviewGroupsResponse{
				Type:            pb.WatchEventType_WATCH_EVENT_TYPE_ADDED,
				PreviewGroup:    dom.ToProto(),
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
				return connect.NewError(connect.CodeResourceExhausted, errors.New("event buffer overflow, please reconnect"))
			}

			if namespace != "" && event.Object.Namespace != namespace {
				continue
			}

			dom, err := CRDPgToDomain(event.Object)
			if err != nil || dom == nil {
				continue
			}

			var eventType pb.WatchEventType
			switch event.Type {
			case "ADDED":
				eventType = pb.WatchEventType_WATCH_EVENT_TYPE_ADDED
			case "MODIFIED":
				eventType = pb.WatchEventType_WATCH_EVENT_TYPE_MODIFIED
			case "DELETED":
				eventType = pb.WatchEventType_WATCH_EVENT_TYPE_DELETED
			default:
				eventType = pb.WatchEventType_WATCH_EVENT_TYPE_UNSPECIFIED
			}

			if err := stream.Send(&pb.WatchPreviewGroupsResponse{
				Type:            eventType,
				PreviewGroup:    dom.ToProto(),
				ResourceVersion: event.Version,
			}); err != nil {
				return err
			}
		}
	}
}
