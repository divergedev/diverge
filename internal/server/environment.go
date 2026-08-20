package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/api/v1alpha1"

	"github.com/divergedev/diverge/internal/server/streaming"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/labels"
)

type EnvironmentService struct {
	client          client.Client
	k8sClient       kubernetes.Interface
	informerMgr     *streaming.InformerManager
	logStreamer     *streaming.LogStreamer
	streamSemaphore chan struct{}
	logger          *slog.Logger
	auditLogger     *AuditLogger
}

func NewEnvironmentService(c client.Client, k8s kubernetes.Interface, informerMgr *streaming.InformerManager, logStreamer *streaming.LogStreamer, sem chan struct{}, logger *slog.Logger, audit *AuditLogger) divergev1alpha1connect.EnvironmentServiceHandler {
	return &EnvironmentService{
		client:          c,
		k8sClient:       k8s,
		informerMgr:     informerMgr,
		logStreamer:     logStreamer,
		streamSemaphore: sem,
		logger:          logger,
		auditLogger:     audit,
	}
}

func (s *EnvironmentService) CreateEnvironment(ctx context.Context, req *connect.Request[pb.CreateEnvironmentRequest]) (*connect.Response[pb.CreateEnvironmentResponse], error) {
	msg := req.Msg
	if msg.Environment == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("environment is required"))
	}

	namespace := msg.Namespace
	if namespace == "" {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}

	// Validate namespace match
	if msg.Environment.Name != "" {
		if err := ValidateDNS1123Label(msg.Environment.Name, "name"); err != nil {
			return nil, err
		}
	}
	if msg.Environment.Namespace != "" {
		if err := ValidateNamespaceMatch(namespace, msg.Environment.Namespace); err != nil {
			return nil, err
		}
	}

	// RBAC check
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "create", namespace, "environments"); err != nil {
		return nil, err
	}

	realCrd, err := ProtoEnvToCRD(msg.Environment)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal server error"))
	}

	realCrd.Namespace = namespace

	if err := s.client.Create(ctx, realCrd); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	s.auditLogger.LogMutation(ctx, "resource.created", "environment", realCrd.Name, realCrd.Namespace)

	domBack, _ := CRDEnvToProto(realCrd)
	return connect.NewResponse(&pb.CreateEnvironmentResponse{
		Environment: domBack,
	}), nil
}

func (s *EnvironmentService) GetEnvironment(ctx context.Context, req *connect.Request[pb.GetEnvironmentRequest]) (*connect.Response[pb.GetEnvironmentResponse], error) {
	if err := ValidateDNS1123Label(req.Msg.Name, "name"); err != nil {
		return nil, err
	}
	if err := ValidateDNS1123Label(req.Msg.Namespace, "namespace"); err != nil {
		return nil, err
	}

	// RBAC check
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "get", req.Msg.Namespace, "environments"); err != nil {
		return nil, err
	}

	var crd v1alpha1.Environment
	if err := s.client.Get(ctx, client.ObjectKey{Name: req.Msg.Name, Namespace: req.Msg.Namespace}, &crd); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}
	dom, err := CRDEnvToProto(&crd)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal server error"))
	}
	return connect.NewResponse(&pb.GetEnvironmentResponse{
		Environment: dom,
	}), nil
}

func (s *EnvironmentService) ListEnvironments(ctx context.Context, req *connect.Request[pb.ListEnvironmentsRequest]) (*connect.Response[pb.ListEnvironmentsResponse], error) {
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
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "list", namespace, "environments"); err != nil {
		return nil, err
	}

	var list v1alpha1.EnvironmentList
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

	var pbs []*pb.Environment
	for i := range list.Items {
		dom, err := CRDEnvToProto(&list.Items[i])
		if err != nil {
			s.logger.Warn("mapper error", "resource", list.Items[i].Name, "error", err)
			continue
		}
		if dom != nil {
			pbs = append(pbs, dom)
		}
	}

	return connect.NewResponse(&pb.ListEnvironmentsResponse{
		Environments:  pbs,
		NextPageToken: list.Continue,
	}), nil
}

func (s *EnvironmentService) UpdateEnvironment(ctx context.Context, req *connect.Request[pb.UpdateEnvironmentRequest]) (*connect.Response[pb.UpdateEnvironmentResponse], error) {
	msg := req.Msg
	if msg.Environment == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("environment is required"))
	}

	if msg.Environment.ResourceVersion == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("resource_version is required; fetch the current resource first and include its resource_version to prevent concurrent modification"))
	}

	// Validate name
	if msg.Environment.Name != "" {
		if err := ValidateDNS1123Label(msg.Environment.Name, "name"); err != nil {
			return nil, err
		}
	}

	namespace := msg.Environment.Namespace
	if namespace == "" {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}

	// RBAC check
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "update", namespace, "environments"); err != nil {
		return nil, err
	}

	var existingCrd v1alpha1.Environment
	if err := s.client.Get(ctx, client.ObjectKey{Name: msg.Environment.Name, Namespace: namespace}, &existingCrd); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	if msg.Environment.ResourceVersion != "" {
		existingCrd.ResourceVersion = msg.Environment.ResourceVersion
	}

	newCrd, err := ProtoEnvToCRD(msg.Environment)
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
		s.logger.Info("partial update applied", "resource", "environment", "name", existingCrd.Name, "paths", msg.UpdateMask.Paths)
	}

	s.auditLogger.LogMutation(ctx, "resource.updated", "environment", existingCrd.Name, existingCrd.Namespace)

	domBack, _ := CRDEnvToProto(&existingCrd)
	if domBack == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal server error"))
	}
	return connect.NewResponse(&pb.UpdateEnvironmentResponse{
		Environment: domBack,
	}), nil
}

func (s *EnvironmentService) DeleteEnvironment(ctx context.Context, req *connect.Request[pb.DeleteEnvironmentRequest]) (*connect.Response[pb.DeleteEnvironmentResponse], error) {
	if err := ValidateDNS1123Label(req.Msg.Name, "name"); err != nil {
		return nil, err
	}
	if err := ValidateDNS1123Label(req.Msg.Namespace, "namespace"); err != nil {
		return nil, err
	}

	// RBAC check
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "delete", req.Msg.Namespace, "environments"); err != nil {
		return nil, err
	}

	var crd v1alpha1.Environment
	crd.Name = req.Msg.Name
	crd.Namespace = req.Msg.Namespace
	if err := s.client.Delete(ctx, &crd); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	s.auditLogger.LogMutation(ctx, "resource.deleted", "environment", req.Msg.Name, req.Msg.Namespace)

	return connect.NewResponse(&pb.DeleteEnvironmentResponse{}), nil
}

func (s *EnvironmentService) ExtendTTL(ctx context.Context, req *connect.Request[pb.ExtendTTLRequest]) (*connect.Response[pb.ExtendTTLResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("unimplemented"))
}

func (s *EnvironmentService) WatchEnvironments(ctx context.Context, req *connect.Request[pb.WatchEnvironmentsRequest], stream *connect.ServerStream[pb.WatchEnvironmentsResponse]) error {
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
		if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "watch", namespace, "environments"); err != nil {
			return err
		}
	} else {
		// Cluster-wide watch: check watch permission at cluster scope (empty namespace)
		if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "watch", "", "environments"); err != nil {
			return err
		}
	}

	// Subscribe FIRST to prevent race condition (Subscribe → List → deduplicate)
	sub := s.informerMgr.EnvBroadcaster.Subscribe(ctx)
	defer s.informerMgr.EnvBroadcaster.Unsubscribe(sub.ID())

	// List current state
	var list v1alpha1.EnvironmentList
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := s.client.List(ctx, &list, opts...); err != nil {
		return SanitizeK8sError(s.logger, err)
	}

	// Track resource versions sent during initial List for deduplication
	sentRVs := make(map[string]struct{}, len(list.Items))

	// Send current state as ADDED
	for i := range list.Items {
		crd := &list.Items[i]
		dom, _ := CRDEnvToProto(crd)
		if dom != nil {
			sentRVs[crd.Namespace+"/"+crd.Name+"@"+crd.ResourceVersion] = struct{}{}
			if err := stream.Send(&pb.WatchEnvironmentsResponse{
				Type:            pb.WatchEventType_WATCH_EVENT_TYPE_ADDED,
				Environment:     dom,
				ResourceVersion: crd.ResourceVersion,
			}); err != nil {
				return err
			}
		}
	}

	// Stream deltas, deduplicating events already covered by the List
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

			dom, err := CRDEnvToProto(event.Object)
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

			if err := stream.Send(&pb.WatchEnvironmentsResponse{
				Type:            eventType,
				Environment:     dom,
				ResourceVersion: event.Version,
			}); err != nil {
				return err
			}
		}
	}
}

type logMessage struct {
	pod       string
	container string
	content   string
	timestamp *timestamppb.Timestamp
}

func (s *EnvironmentService) StreamLogs(ctx context.Context, req *connect.Request[pb.StreamLogsRequest], stream *connect.ServerStream[pb.StreamLogsResponse]) error {
	if s.logStreamer == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("log streamer is not configured"))
	}

	select {
	case s.streamSemaphore <- struct{}{}:
		defer func() { <-s.streamSemaphore }()
	default:
		return connect.NewError(connect.CodeResourceExhausted, errors.New("too many concurrent streams"))
	}

	msg := req.Msg
	if msg.EnvironmentName == "" || msg.Namespace == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("environment name and namespace are required"))
	}
	if err := ValidateDNS1123Label(msg.Namespace, "namespace"); err != nil {
		return err
	}
	if err := ValidateDNS1123Label(msg.EnvironmentName, "environment_name"); err != nil {
		return err
	}

	// RBAC: check environment read AND pods/log access
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "get", msg.Namespace, "environments"); err != nil {
		return err
	}
	if err := AuthorizePodLogs(ctx, s.k8sClient, s.auditLogger, msg.Namespace); err != nil {
		return err
	}

	opts := []client.ListOption{
		client.InNamespace(msg.Namespace),
		client.MatchingLabels{"diverge.dev/environment": msg.EnvironmentName},
	}
	var podList corev1.PodList
	if err := s.client.List(ctx, &podList, opts...); err != nil {
		return SanitizeK8sError(s.logger, err)
	}

	var targetPods []corev1.Pod
	for _, pod := range podList.Items {
		if msg.ServiceName != "" {
			svcName := pod.Labels["app.kubernetes.io/name"]
			if svcName != "" && svcName != msg.ServiceName {
				continue
			}
		}
		targetPods = append(targetPods, pod)
	}

	if len(targetPods) == 0 {
		return connect.NewError(connect.CodeNotFound, errors.New("no pods found for environment and service"))
	}

	// Cap at MaxStreamLogsPods to prevent goroutine bomb
	if len(targetPods) > MaxStreamLogsPods {
		s.logger.Warn("pod count exceeds StreamLogs limit",
			"requested", len(targetPods),
			"limit", MaxStreamLogsPods,
			"environment", msg.EnvironmentName,
			"namespace", msg.Namespace,
		)
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("too many pods (%d), limit is %d; narrow your request using service_name", len(targetPods), MaxStreamLogsPods))
	}

	logCh := make(chan logMessage, 256) // Bounded buffer with backpressure
	errCh := make(chan error, len(targetPods))

	var wg sync.WaitGroup
	for _, pod := range targetPods {
		wg.Add(1)
		go func(p corev1.Pod) {
			defer wg.Done()

			containerName := msg.Container

			var since *time.Time
			if msg.SinceTime != nil {
				t := msg.SinceTime.AsTime()
				since = &t
			}

			logsStream, err := s.logStreamer.StreamPodLogs(ctx, p.Namespace, p.Name, containerName, msg.Follow, msg.TailLines, since)
			if err != nil {
				errCh <- fmt.Errorf("failed to open stream for pod %s: %w", p.Name, err)
				return
			}
			defer func() {
				_ = logsStream.Close()
			}()

			// Use 64KB initial buffer, allow up to 1MB for long log lines
			scanner := bufio.NewScanner(logsStream)
			buf := make([]byte, 64*1024)
			scanner.Buffer(buf, 1024*1024)

			for scanner.Scan() {
				line := scanner.Text()

				var ts *timestamppb.Timestamp
				content := line
				if msg.Timestamps {
					timestamp, rest, found := strings.Cut(line, " ")
					if found {
						if parsedTime, err := time.Parse(time.RFC3339Nano, timestamp); err == nil {
							ts = timestamppb.New(parsedTime)
							content = rest
						}
					}
				}

				select {
				case <-ctx.Done():
					return
				case logCh <- logMessage{
					pod:       p.Name,
					container: containerName,
					content:   content,
					timestamp: ts,
				}:
				}
			}
			if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- fmt.Errorf("log scan error: %w", err)
			}
		}(pod)
	}

	go func() {
		wg.Wait()
		close(logCh)
	}()

	// Send logs to stream
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			return SanitizeK8sError(s.logger, err)
		case msg, ok := <-logCh:
			if !ok {
				// All streams completed
				return nil
			}

			resp := &pb.StreamLogsResponse{
				PodName:       msg.pod,
				ContainerName: msg.container,
				Content:       msg.content,
				Timestamp:     msg.timestamp,
				ServiceName:   req.Msg.ServiceName,
			}

			if err := stream.Send(resp); err != nil {
				return err
			}
		}
	}
}
