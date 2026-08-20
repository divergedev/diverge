package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/api/v1alpha1"

	"github.com/divergedev/diverge/internal/server/streaming"
	"k8s.io/apimachinery/pkg/labels"
)

type PreviewGroupService struct {
	client          client.Client
	k8sClient       kubernetes.Interface
	informerMgr     *streaming.InformerManager
	streamSemaphore chan struct{}
	logger          *slog.Logger
	auditLogger     *AuditLogger
}

func NewPreviewGroupService(c client.Client, k8s kubernetes.Interface, informerMgr *streaming.InformerManager, sem chan struct{}, logger *slog.Logger, audit *AuditLogger) divergev1alpha1connect.PreviewGroupServiceHandler {
	return &PreviewGroupService{
		client:          c,
		k8sClient:       k8s,
		informerMgr:     informerMgr,
		streamSemaphore: sem,
		logger:          logger,
		auditLogger:     audit,
	}
}

func (s *PreviewGroupService) CreatePreviewGroup(ctx context.Context, req *connect.Request[pb.CreatePreviewGroupRequest]) (*connect.Response[pb.CreatePreviewGroupResponse], error) {
	msg := req.Msg
	if msg.PreviewGroup == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("preview group is required"))
	}

	namespace := msg.Namespace
	if namespace == "" {
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
	if msg.PreviewGroup.Namespace != "" {
		if err := ValidateNamespaceMatch(namespace, msg.PreviewGroup.Namespace); err != nil {
			return nil, err
		}
	}

	// RBAC check
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "create", namespace, "previewgroups"); err != nil {
		return nil, err
	}

	realCrd, err := ProtoPgToCRD(msg.PreviewGroup)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal server error"))
	}

	realCrd.Namespace = namespace

	if err := s.client.Create(ctx, realCrd); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	s.auditLogger.LogMutation(ctx, "resource.created", "previewgroup", realCrd.Name, realCrd.Namespace)

	domBack, _ := CRDPgToProto(realCrd)
	return connect.NewResponse(&pb.CreatePreviewGroupResponse{
		PreviewGroup: domBack,
	}), nil
}

func (s *PreviewGroupService) GetPreviewGroup(ctx context.Context, req *connect.Request[pb.GetPreviewGroupRequest]) (*connect.Response[pb.GetPreviewGroupResponse], error) {
	if err := ValidateDNS1123Label(req.Msg.Name, "name"); err != nil {
		return nil, err
	}
	if err := ValidateDNS1123Label(req.Msg.Namespace, "namespace"); err != nil {
		return nil, err
	}

	// RBAC check
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "get", req.Msg.Namespace, "previewgroups"); err != nil {
		return nil, err
	}

	var crd v1alpha1.PreviewGroup
	if err := s.client.Get(ctx, client.ObjectKey{Name: req.Msg.Name, Namespace: req.Msg.Namespace}, &crd); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
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
	namespace := req.Msg.Namespace
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

	// NOTE: page_token is tied to the original query's label_selector and resource_version.
	// Changing label_selector between pages will result in an error.
	// Tokens may expire if the underlying data changes significantly.

	// RBAC check
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "list", namespace, "previewgroups"); err != nil {
		return nil, err
	}

	var list v1alpha1.PreviewGroupList
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}

	pageSize := req.Msg.PageSize
	if pageSize <= 0 {
		pageSize = 100
	} else if pageSize > 1000 {
		pageSize = 1000
	}
	opts = append(opts, client.Limit(pageSize))

	if req.Msg.PageToken != "" {
		opts = append(opts, client.Continue(req.Msg.PageToken))
	}

	if req.Msg.LabelSelector != "" {
		selector, err := labels.Parse(req.Msg.LabelSelector)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid label_selector: %w", err))
		}
		opts = append(opts, client.MatchingLabelsSelector{Selector: selector})
	}

	if err := s.client.List(ctx, &list, opts...); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	var pbs []*pb.PreviewGroup
	for i := range list.Items {
		dom, err := CRDPgToProto(&list.Items[i])
		if err != nil {
			s.logger.Warn("mapper error", "resource", list.Items[i].Name, "error", err)
			continue
		}
		if dom != nil {
			pbs = append(pbs, dom)
		}
	}

	return connect.NewResponse(&pb.ListPreviewGroupsResponse{
		PreviewGroups: pbs,
		NextPageToken: list.Continue,
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

	namespace := msg.PreviewGroup.Namespace
	if namespace == "" {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}

	// RBAC check
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "update", namespace, "previewgroups"); err != nil {
		return nil, err
	}

	var existingCrd v1alpha1.PreviewGroup
	if err := s.client.Get(ctx, client.ObjectKey{Name: msg.PreviewGroup.Name, Namespace: namespace}, &existingCrd); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	if msg.PreviewGroup.ResourceVersion != "" {
		existingCrd.ResourceVersion = msg.PreviewGroup.ResourceVersion
	}

	newCrd, err := ProtoPgToCRD(msg.PreviewGroup)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal server error"))
	}

	// NOTE: FieldMask currently supports top-level paths only (spec, labels, annotations).
	// Nested paths like "spec.source" are not supported and will be rejected.
	// Full sub-field updates require replacing the entire parent (e.g., path="spec").
	if msg.UpdateMask != nil && len(msg.UpdateMask.Paths) > 0 {
		allowedPaths := map[string]bool{"spec": true, "labels": true, "annotations": true}
		for _, path := range msg.UpdateMask.Paths {
			if !allowedPaths[path] {
				return nil, connect.NewError(connect.CodeInvalidArgument,
					fmt.Errorf("unsupported field mask path: %q; allowed paths are: spec, labels, annotations", path))
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

	if err := s.client.Update(ctx, &existingCrd); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	if msg.UpdateMask != nil && len(msg.UpdateMask.Paths) > 0 {
		s.logger.Info("partial update applied", "resource", "previewgroup", "name", existingCrd.Name, "paths", msg.UpdateMask.Paths)
	}

	s.auditLogger.LogMutation(ctx, "resource.updated", "previewgroup", existingCrd.Name, existingCrd.Namespace)

	domBack, _ := CRDPgToProto(&existingCrd)
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
	if err := ValidateDNS1123Label(req.Msg.Namespace, "namespace"); err != nil {
		return nil, err
	}

	// RBAC check
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "delete", req.Msg.Namespace, "previewgroups"); err != nil {
		return nil, err
	}

	var crd v1alpha1.PreviewGroup
	crd.Name = req.Msg.Name
	crd.Namespace = req.Msg.Namespace
	if err := s.client.Delete(ctx, &crd); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	s.auditLogger.LogMutation(ctx, "resource.deleted", "previewgroup", req.Msg.Name, req.Msg.Namespace)

	return connect.NewResponse(&pb.DeletePreviewGroupResponse{}), nil
}

func (s *PreviewGroupService) WatchPreviewGroups(ctx context.Context, req *connect.Request[pb.WatchPreviewGroupsRequest], stream *connect.ServerStream[pb.WatchPreviewGroupsResponse]) error {
	if s.informerMgr == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("informer manager is not configured"))
	}

	select {
	case s.streamSemaphore <- struct{}{}:
		defer func() { <-s.streamSemaphore }()
	default:
		return connect.NewError(connect.CodeResourceExhausted, errors.New("too many concurrent streams"))
	}

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

	// Subscribe FIRST to prevent race condition (Subscribe → List → deduplicate)
	sub := s.informerMgr.PgBroadcaster.Subscribe(ctx)
	defer s.informerMgr.PgBroadcaster.Unsubscribe(sub.ID())

	// List current state
	var list v1alpha1.PreviewGroupList
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := s.client.List(ctx, &list, opts...); err != nil {
		return SanitizeK8sError(s.logger, err)
	}

	// Track resource versions sent during initial List for deduplication
	sentRVs := make(map[string]struct{}, len(list.Items))

	for i := range list.Items {
		crd := &list.Items[i]
		dom, _ := CRDPgToProto(crd)
		if dom != nil {
			sentRVs[crd.Namespace+"/"+crd.Name+"@"+crd.ResourceVersion] = struct{}{}
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
				return connect.NewError(connect.CodeResourceExhausted, errors.New("event buffer overflow, please reconnect"))
			}

			// Deduplicate: skip events already sent during the initial List
			if event.Object != nil && event.Version != "" {
				key := event.Object.Namespace + "/" + event.Object.Name + "@" + event.Version
				if _, sent := sentRVs[key]; sent {
					continue
				}
			}

			if namespace != "" && event.Object.Namespace != namespace {
				continue
			}

			dom, err := CRDPgToProto(event.Object)
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
				PreviewGroup:    dom,
				ResourceVersion: event.Version,
			}); err != nil {
				return err
			}
		}
	}
}
