package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/api/v1alpha1"
	domain "github.com/divergedev/diverge/gen/domain/github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/internal/server/streaming"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type EnvironmentService struct {
	client      client.Client
	informerMgr *streaming.InformerManager
	logStreamer *streaming.LogStreamer
}

func NewEnvironmentService(c client.Client, informerMgr *streaming.InformerManager, logStreamer *streaming.LogStreamer) divergev1alpha1connect.EnvironmentServiceHandler {
	return &EnvironmentService{
		client:      c,
		informerMgr: informerMgr,
		logStreamer: logStreamer,
	}
}

func (s *EnvironmentService) CreateEnvironment(ctx context.Context, req *connect.Request[pb.CreateEnvironmentRequest]) (*connect.Response[pb.CreateEnvironmentResponse], error) {
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

	realCrd.Namespace = msg.Namespace
	if realCrd.Namespace == "" {
		realCrd.Namespace = "default"
	}

	if err := s.client.Create(ctx, realCrd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

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

func (s *EnvironmentService) authorizeAction(ctx context.Context, verb, namespace string) error {
	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      verb,
				Group:     "diverge.dev",
				Resource:  "environments",
			},
		},
	}
	if err := s.client.Create(ctx, sar); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authorization check failed: %w", err))
	}
	if !sar.Status.Allowed {
		return connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}
	return nil
}

func (s *EnvironmentService) WatchEnvironments(ctx context.Context, req *connect.Request[pb.WatchEnvironmentsRequest], stream *connect.ServerStream[pb.WatchEnvironmentsResponse]) error {
	if s.informerMgr == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("informer manager is not configured"))
	}

	select {
	case StreamSemaphore <- struct{}{}:
		defer func() { <-StreamSemaphore }()
	default:
		return connect.NewError(connect.CodeResourceExhausted, errors.New("too many concurrent streams"))
	}

	streamCtx, cancel := context.WithTimeout(ctx, 4*time.Hour)
	defer cancel()

	namespace := req.Msg.Namespace
	if namespace != "" {
		if err := s.authorizeAction(streamCtx, "watch", namespace); err != nil {
			return err
		}
	}

	// 1. List current state to get latestRV
	var list v1alpha1.EnvironmentList
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := s.client.List(streamCtx, &list, opts...); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	// 2. Subscribe AFTER getting the list
	sub := s.informerMgr.EnvBroadcaster.Subscribe(streamCtx)
	defer s.informerMgr.EnvBroadcaster.Unsubscribe(sub.ID())

	// 3. Send current state as ADDED
	for i := range list.Items {
		crd := &list.Items[i]
		dom, _ := CRDEnvToDomain(crd)
		if dom != nil {
			if err := stream.Send(&pb.WatchEnvironmentsResponse{
				Type:            pb.WatchEventType_WATCH_EVENT_TYPE_ADDED,
				Environment:     dom.ToProto(),
				ResourceVersion: crd.ResourceVersion,
			}); err != nil {
				return err
			}
		}
	}

	// 4. Stream deltas
	for {
		select {
		case <-streamCtx.Done():
			return nil
		case event, ok := <-sub.Events():
			if !ok {
				return connect.NewError(connect.CodeResourceExhausted, errors.New("event buffer overflow, please reconnect"))
			}

			if namespace != "" && event.Object.Namespace != namespace {
				continue
			}

			dom, err := CRDEnvToDomain(event.Object)
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
				Environment:     dom.ToProto(),
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
	case StreamSemaphore <- struct{}{}:
		defer func() { <-StreamSemaphore }()
	default:
		return connect.NewError(connect.CodeResourceExhausted, errors.New("too many concurrent streams"))
	}

	streamCtx, cancel := context.WithTimeout(ctx, 4*time.Hour)
	defer cancel()

	msg := req.Msg
	if msg.EnvironmentName == "" || msg.Namespace == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("environment name and namespace are required"))
	}

	if err := s.authorizeAction(streamCtx, "get", msg.Namespace); err != nil {
		return err
	}

	opts := []client.ListOption{
		client.InNamespace(msg.Namespace),
		client.MatchingLabels{"diverge.dev/environment": msg.EnvironmentName},
	}
	var podList corev1.PodList
	if err := s.client.List(streamCtx, &podList, opts...); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list pods: %w", err))
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

	logCh := make(chan logMessage, 100)
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

			logsStream, err := s.logStreamer.StreamPodLogs(streamCtx, p.Namespace, p.Name, containerName, msg.Follow, msg.TailLines, since)
			if err != nil {
				errCh <- fmt.Errorf("failed to open stream for pod %s: %w", p.Name, err)
				return
			}
			defer func() {
				_ = logsStream.Close()
			}()

			scanner := bufio.NewScanner(logsStream)
			scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
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
				case <-streamCtx.Done():
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
		case <-streamCtx.Done():
			return nil
		case err := <-errCh:
			return connect.NewError(connect.CodeInternal, err)
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
				ServiceName:   req.Msg.ServiceName, // Echo back what they asked for, or parse from pod labels
			}

			if err := stream.Send(resp); err != nil {
				return err
			}
		}
	}
}
