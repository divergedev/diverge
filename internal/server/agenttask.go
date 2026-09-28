package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server/streaming"

	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AgentTaskService implements the ConnectRPC divergev1alpha1connect.AgentTaskServiceHandler interface.
type AgentTaskService struct {
	client      client.Client
	k8sClient   kubernetes.Interface
	logStreamer *streaming.LogStreamer
	limiter     *StreamLimiter
	logger      *slog.Logger
	auditLogger *AuditLogger
}

var _ divergev1alpha1connect.AgentTaskServiceHandler = (*AgentTaskService)(nil)

// NewAgentTaskService initializes a new AgentTaskService.
func NewAgentTaskService(
	c client.Client,
	k8s kubernetes.Interface,
	logStreamer *streaming.LogStreamer,
	limiter *StreamLimiter,
	logger *slog.Logger,
	audit *AuditLogger,
) *AgentTaskService {
	if logger == nil {
		logger = slog.Default()
	}
	return &AgentTaskService{
		client:      c,
		k8sClient:   k8s,
		logStreamer: logStreamer,
		limiter:     limiter,
		logger:      logger,
		auditLogger: audit,
	}
}

// CreateTask initiates a new autonomous AgentTask.
func (s *AgentTaskService) CreateTask(ctx context.Context, req *connect.Request[pb.CreateTaskRequest]) (*connect.Response[pb.CreateTaskResponse], error) {
	msg := req.Msg
	if msg == nil || msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("task name is required"))
	}
	namespace := msg.Namespace
	if namespace == "" {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}
	if err := ValidateDNS1123Label(msg.Name, "name"); err != nil {
		return nil, err
	}

	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "create", namespace, "agenttasks"); err != nil {
		return nil, err
	}

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      msg.Name,
			Namespace: namespace,
		},
	}
	if msg.Spec != nil {
		task.Spec = v1alpha1.AgentTaskSpec{
			Objective:     msg.Spec.Objective,
			BudgetUSD:     msg.Spec.BudgetUsd,
			BudgetTokens:  msg.Spec.BudgetTokens,
			MaxIterations: msg.Spec.MaxIterations,
			Capabilities:  msg.Spec.Capabilities,
			DraftPR:       msg.Spec.DraftPr,
			Suspended:     msg.Spec.Suspended,
		}
		if msg.Spec.Repository != nil {
			task.Spec.Repository = v1alpha1.AgentTaskRepository{
				URL:           msg.Spec.Repository.Url,
				BaseBranch:    msg.Spec.Repository.BaseBranch,
				WorkingBranch: msg.Spec.Repository.WorkingBranch,
			}
		}
		if msg.Spec.Sandbox != nil {
			task.Spec.Sandbox = v1alpha1.AgentTaskSandbox{
				Provider:       msg.Spec.Sandbox.Provider,
				PoolRef:        msg.Spec.Sandbox.PoolRef,
				TemplateRef:    msg.Spec.Sandbox.TemplateRef,
				TimeoutSeconds: msg.Spec.Sandbox.TimeoutSeconds,
			}
		}
	}

	if err := s.client.Create(ctx, task); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("agent task %q already exists", msg.Name))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create agent task: %w", err))
	}

	return connect.NewResponse(&pb.CreateTaskResponse{
		Task: toProtoAgentTask(task),
	}), nil
}

// GetTask retrieves an AgentTask by namespace and name.
func (s *AgentTaskService) GetTask(ctx context.Context, req *connect.Request[pb.GetTaskRequest]) (*connect.Response[pb.GetTaskResponse], error) {
	msg := req.Msg
	if msg == nil || msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("task name is required"))
	}
	namespace := msg.Namespace
	if namespace == "" {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}
	if err := ValidateDNS1123Label(msg.Name, "name"); err != nil {
		return nil, err
	}

	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "get", namespace, "agenttasks"); err != nil {
		return nil, err
	}

	task := &v1alpha1.AgentTask{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: msg.Name}, task); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("agent task %q not found", msg.Name))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get agent task: %w", err))
	}

	return connect.NewResponse(&pb.GetTaskResponse{
		Task: toProtoAgentTask(task),
	}), nil
}

// ListTasks returns a list of AgentTasks in a given namespace.
func (s *AgentTaskService) ListTasks(ctx context.Context, req *connect.Request[pb.ListTasksRequest]) (*connect.Response[pb.ListTasksResponse], error) {
	msg := req.Msg
	namespace := ""
	if msg != nil {
		namespace = msg.Namespace
	}
	if namespace == "" {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}

	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "list", namespace, "agenttasks"); err != nil {
		return nil, err
	}

	taskList := &v1alpha1.AgentTaskList{}
	if err := s.client.List(ctx, taskList, client.InNamespace(namespace)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list agent tasks: %w", err))
	}

	protoTasks := make([]*pb.AgentTask, 0, len(taskList.Items))
	for i := range taskList.Items {
		protoTasks = append(protoTasks, toProtoAgentTask(&taskList.Items[i]))
	}

	return connect.NewResponse(&pb.ListTasksResponse{
		Tasks: protoTasks,
	}), nil
}

// DeleteTask removes an AgentTask and initiates teardown.
func (s *AgentTaskService) DeleteTask(ctx context.Context, req *connect.Request[pb.DeleteTaskRequest]) (*connect.Response[pb.DeleteTaskResponse], error) {
	msg := req.Msg
	if msg == nil || msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("task name is required"))
	}
	namespace := msg.Namespace
	if namespace == "" {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}
	if err := ValidateDNS1123Label(msg.Name, "name"); err != nil {
		return nil, err
	}

	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "delete", namespace, "agenttasks"); err != nil {
		return nil, err
	}

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      msg.Name,
			Namespace: namespace,
		},
	}
	if err := s.client.Delete(ctx, task); err != nil && !apierrors.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("delete agent task: %w", err))
	}

	return connect.NewResponse(&pb.DeleteTaskResponse{}), nil
}

// PauseTask pauses execution of an active AgentTask.
func (s *AgentTaskService) PauseTask(ctx context.Context, req *connect.Request[pb.PauseTaskRequest]) (*connect.Response[pb.PauseTaskResponse], error) {
	msg := req.Msg
	if msg == nil || msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("task name is required"))
	}
	namespace := msg.Namespace
	if namespace == "" {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}

	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "update", namespace, "agenttasks"); err != nil {
		return nil, err
	}

	task := &v1alpha1.AgentTask{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: msg.Name}, task); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("agent task %q not found", msg.Name))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	task.Spec.Suspended = true
	if err := s.client.Update(ctx, task); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("pause agent task: %w", err))
	}

	return connect.NewResponse(&pb.PauseTaskResponse{
		Task: toProtoAgentTask(task),
	}), nil
}

// ResumeTask resumes execution of a paused AgentTask with optional budget bumps.
func (s *AgentTaskService) ResumeTask(ctx context.Context, req *connect.Request[pb.ResumeTaskRequest]) (*connect.Response[pb.ResumeTaskResponse], error) {
	msg := req.Msg
	if msg == nil || msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("task name is required"))
	}
	namespace := msg.Namespace
	if namespace == "" {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}

	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "update", namespace, "agenttasks"); err != nil {
		return nil, err
	}

	task := &v1alpha1.AgentTask{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: msg.Name}, task); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("agent task %q not found", msg.Name))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	task.Spec.Suspended = false
	if msg.BudgetUsdBump != "" {
		task.Spec.BudgetUSD = msg.BudgetUsdBump
	}
	if msg.BudgetTokensBump > 0 {
		task.Spec.BudgetTokens = msg.BudgetTokensBump
	}

	if err := s.client.Update(ctx, task); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("resume agent task: %w", err))
	}

	return connect.NewResponse(&pb.ResumeTaskResponse{
		Task: toProtoAgentTask(task),
	}), nil
}

// GuideTask delivers steering instructions to an active AgentTask.
func (s *AgentTaskService) GuideTask(ctx context.Context, req *connect.Request[pb.GuideTaskRequest]) (*connect.Response[pb.GuideTaskResponse], error) {
	msg := req.Msg
	if msg == nil || msg.Name == "" || msg.Message == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("task name and message are required"))
	}
	namespace := msg.Namespace
	if namespace == "" {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}

	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "update", namespace, "agenttasks"); err != nil {
		return nil, err
	}

	task := &v1alpha1.AgentTask{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: msg.Name}, task); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("agent task %q not found", msg.Name))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if task.Annotations == nil {
		task.Annotations = make(map[string]string)
	}
	task.Annotations["divergedev.com/guidance"] = msg.Message
	task.Annotations["divergedev.com/guidance-timestamp"] = time.Now().UTC().Format(time.RFC3339)

	if err := s.client.Update(ctx, task); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("guide agent task: %w", err))
	}

	return connect.NewResponse(&pb.GuideTaskResponse{Accepted: true}), nil
}

// StreamTaskLogs streams output from the agent's sandbox pod.
func (s *AgentTaskService) StreamTaskLogs(
	ctx context.Context,
	req *connect.Request[pb.StreamTaskLogsRequest],
	stream *connect.ServerStream[pb.StreamTaskLogsResponse],
) error {
	msg := req.Msg
	if msg == nil || msg.Name == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("task name is required"))
	}
	namespace := msg.Namespace
	if namespace == "" {
		namespace = "default"
	}

	task := &v1alpha1.AgentTask{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: msg.Name}, task); err != nil {
		if apierrors.IsNotFound(err) {
			return connect.NewError(connect.CodeNotFound, fmt.Errorf("agent task %q not found", msg.Name))
		}
		return connect.NewError(connect.CodeInternal, err)
	}

	podName := task.Status.SandboxPodName
	if podName == "" {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("sandbox pod not yet bound for task %s", msg.Name))
	}

	if err := AuthorizePodLogs(ctx, s.k8sClient, s.auditLogger, namespace); err != nil {
		return err
	}

	if s.limiter != nil {
		release, err := s.limiter.Acquire(ctx)
		if err != nil {
			return connect.NewError(connect.CodeResourceExhausted, err)
		}
		defer release()
	}

	if s.k8sClient == nil {
		return stream.Send(&pb.StreamTaskLogsResponse{
			Chunk:     []byte("k8sClient unavailable for logs\n"),
			Timestamp: timestamppb.Now(),
		})
	}

	logOpts := &corev1.PodLogOptions{
		Follow: msg.Follow,
	}
	if msg.TailLines > 0 {
		logOpts.TailLines = &msg.TailLines
	}

	reqStream := s.k8sClient.CoreV1().Pods(namespace).GetLogs(podName, logOpts)
	readCloser, err := reqStream.Stream(ctx)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("open pod log stream: %w", err))
	}
	defer func() { _ = readCloser.Close() }()

	reader := bufio.NewReaderSize(readCloser, 16*1024)
	buf := make([]byte, 16*1024)
	for {
		n, rErr := reader.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if sendErr := stream.Send(&pb.StreamTaskLogsResponse{
				Chunk:     chunk,
				Timestamp: timestamppb.Now(),
			}); sendErr != nil {
				return sendErr
			}
		}
		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				break
			}
			return connect.NewError(connect.CodeInternal, fmt.Errorf("read pod log stream: %w", rErr))
		}
	}

	return nil
}

func toProtoAgentTask(task *v1alpha1.AgentTask) *pb.AgentTask {
	if task == nil {
		return nil
	}
	p := &pb.AgentTask{
		Name:      task.Name,
		Namespace: task.Namespace,
		CreatedAt: timestamppb.New(task.CreationTimestamp.Time),
		Spec: &pb.AgentTaskSpec{
			Objective:     task.Spec.Objective,
			BudgetUsd:     task.Spec.BudgetUSD,
			BudgetTokens:  task.Spec.BudgetTokens,
			MaxIterations: task.Spec.MaxIterations,
			Capabilities:  task.Spec.Capabilities,
			DraftPr:       task.Spec.DraftPR,
			Suspended:     task.Spec.Suspended,
			Repository: &pb.AgentTaskRepository{
				Url:           task.Spec.Repository.URL,
				BaseBranch:    task.Spec.Repository.BaseBranch,
				WorkingBranch: task.Spec.Repository.WorkingBranch,
			},
			Sandbox: &pb.AgentTaskSandbox{
				Provider:       task.Spec.Sandbox.Provider,
				PoolRef:        task.Spec.Sandbox.PoolRef,
				TemplateRef:    task.Spec.Sandbox.TemplateRef,
				TimeoutSeconds: task.Spec.Sandbox.TimeoutSeconds,
			},
		},
		Status: &pb.AgentTaskStatus{
			SandboxClaimRef:    task.Status.SandboxClaimRef,
			SandboxPodName:     task.Status.SandboxPodName,
			SandboxIp:          task.Status.SandboxIP,
			Iteration:          task.Status.Iteration,
			PrUrl:              task.Status.PRURL,
			CostUsd:            task.Status.CostUSD,
			TokensConsumed:     task.Status.TokensConsumed,
			Message:            task.Status.Message,
			ObservedGeneration: task.Status.ObservedGeneration,
		},
	}

	switch task.Status.Phase {
	case v1alpha1.AgentTaskPhasePending:
		p.Status.Phase = pb.AgentTaskPhase_AGENT_TASK_PHASE_PENDING
	case v1alpha1.AgentTaskPhaseProvisioning:
		p.Status.Phase = pb.AgentTaskPhase_AGENT_TASK_PHASE_PROVISIONING
	case v1alpha1.AgentTaskPhaseActive:
		p.Status.Phase = pb.AgentTaskPhase_AGENT_TASK_PHASE_ACTIVE
	case v1alpha1.AgentTaskPhasePausing:
		p.Status.Phase = pb.AgentTaskPhase_AGENT_TASK_PHASE_PAUSING
	case v1alpha1.AgentTaskPhasePaused:
		p.Status.Phase = pb.AgentTaskPhase_AGENT_TASK_PHASE_PAUSED
	case v1alpha1.AgentTaskPhaseCompleted:
		p.Status.Phase = pb.AgentTaskPhase_AGENT_TASK_PHASE_COMPLETED
	case v1alpha1.AgentTaskPhaseFailed:
		p.Status.Phase = pb.AgentTaskPhase_AGENT_TASK_PHASE_FAILED
	default:
		p.Status.Phase = pb.AgentTaskPhase_AGENT_TASK_PHASE_UNSPECIFIED
	}

	return p
}
