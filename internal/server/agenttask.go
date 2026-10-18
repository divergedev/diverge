package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strconv"
	"strings"
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
	"k8s.io/client-go/util/retry"
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

	if msg.Spec == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("task spec is required"))
	}
	if msg.Spec.Objective == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("task objective is required"))
	}
	if msg.Spec.Repository == nil || msg.Spec.Repository.Url == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("repository url is required"))
	}
	if msg.Spec.BudgetUsd != "" {
		budget, err := strconv.ParseFloat(msg.Spec.BudgetUsd, 64)
		if err != nil || math.IsNaN(budget) || math.IsInf(budget, 0) || budget < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("budget_usd must be a non-negative number"))
		}
	}
	if msg.Spec.BudgetTokens < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("budget_tokens must be non-negative"))
	}

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      msg.Name,
			Namespace: namespace,
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective:     msg.Spec.Objective,
			BudgetUSD:     msg.Spec.BudgetUsd,
			BudgetTokens:  msg.Spec.BudgetTokens,
			MaxIterations: msg.Spec.MaxIterations,
			Capabilities:  msg.Spec.Capabilities,
			DraftPR:       msg.Spec.DraftPr,
			Suspended:     msg.Spec.Suspended,
			Repository: v1alpha1.AgentTaskRepository{
				URL:           msg.Spec.Repository.Url,
				BaseBranch:    msg.Spec.Repository.BaseBranch,
				WorkingBranch: msg.Spec.Repository.WorkingBranch,
			},
		},
	}
	if msg.Spec.Sandbox != nil {
		task.Spec.Sandbox = v1alpha1.AgentTaskSandbox{
			Provider:       msg.Spec.Sandbox.Provider,
			PoolRef:        msg.Spec.Sandbox.PoolRef,
			TemplateRef:    msg.Spec.Sandbox.TemplateRef,
			TimeoutSeconds: msg.Spec.Sandbox.TimeoutSeconds,
		}
	}

	if err := s.client.Create(ctx, task); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	if s.auditLogger != nil {
		s.auditLogger.LogMutation(ctx, "resource.created", "agenttask", task.Name, task.Namespace)
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
	if err := AuthorizeTaskObjectAccess(ctx, s.auditLogger, "get", namespace, msg.Name); err != nil {
		return nil, err
	}

	task := &v1alpha1.AgentTask{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: msg.Name}, task); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	return connect.NewResponse(&pb.GetTaskResponse{
		Task: toProtoAgentTask(task),
	}), nil
}

// ListTasks returns a list of AgentTasks in a given namespace (or across all namespaces when namespace is empty).
func (s *AgentTaskService) ListTasks(ctx context.Context, req *connect.Request[pb.ListTasksRequest]) (*connect.Response[pb.ListTasksResponse], error) {
	msg := req.Msg
	namespace := ""
	var pageSize int32
	var pageToken string
	if msg != nil {
		namespace = msg.Namespace
		if msg.Pagination != nil {
			pageSize = msg.Pagination.PageSize
			pageToken = msg.Pagination.PageToken
		}
	}
	if namespace != "" {
		if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
			return nil, err
		}
	}
	const maxPageTokenLen = 4096
	if len(pageToken) > maxPageTokenLen {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("page_token exceeds maximum length of %d", maxPageTokenLen))
	}

	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "list", namespace, "agenttasks"); err != nil {
		return nil, err
	}

	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if pageSize <= 0 {
		pageSize = 100
	} else if pageSize > 1000 {
		pageSize = 1000
	}
	opts = append(opts, client.Limit(int64(pageSize)))
	if pageToken != "" {
		opts = append(opts, client.Continue(pageToken))
	}

	taskList := &v1alpha1.AgentTaskList{}
	if err := s.client.List(ctx, taskList, opts...); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	protoTasks := make([]*pb.AgentTask, 0, len(taskList.Items))
	for i := range taskList.Items {
		protoTasks = append(protoTasks, toProtoAgentTask(&taskList.Items[i]))
	}

	return connect.NewResponse(&pb.ListTasksResponse{
		Tasks: protoTasks,
		Pagination: &pb.PaginationResponse{
			NextPageToken: taskList.Continue,
		},
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
		return nil, SanitizeK8sError(s.logger, err)
	}

	if s.auditLogger != nil {
		s.auditLogger.LogMutation(ctx, "resource.deleted", "agenttask", msg.Name, namespace)
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
	if err := ValidateDNS1123Label(msg.Name, "name"); err != nil {
		return nil, err
	}

	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "update", namespace, "agenttasks"); err != nil {
		return nil, err
	}

	var updatedTask *v1alpha1.AgentTask
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		task := &v1alpha1.AgentTask{}
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: msg.Name}, task); err != nil {
			return err
		}
		task.Spec.Suspended = true
		if err := s.client.Update(ctx, task); err != nil {
			return err
		}
		updatedTask = task
		return nil
	})
	if err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	if s.auditLogger != nil {
		s.auditLogger.LogMutation(ctx, "resource.paused", "agenttask", msg.Name, namespace)
	}

	return connect.NewResponse(&pb.PauseTaskResponse{
		Task: toProtoAgentTask(updatedTask),
	}), nil
}

func normalizeBudgetBumpString(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "+")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "$")
	s = strings.TrimPrefix(s, "+")
	return strings.TrimSpace(s)
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
	if err := ValidateDNS1123Label(msg.Name, "name"); err != nil {
		return nil, err
	}
	if msg.BudgetTokensBump < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("budget_tokens_bump must be non-negative"))
	}
	var bumpUSD float64
	cleanBump := normalizeBudgetBumpString(msg.BudgetUsdBump)
	if cleanBump != "" {
		var err error
		bumpUSD, err = strconv.ParseFloat(cleanBump, 64)
		if err != nil || math.IsNaN(bumpUSD) || math.IsInf(bumpUSD, 0) || bumpUSD <= 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("budget_usd_bump must be a positive number"))
		}
	}

	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "update", namespace, "agenttasks"); err != nil {
		return nil, err
	}

	var updatedTask *v1alpha1.AgentTask
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		task := &v1alpha1.AgentTask{}
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: msg.Name}, task); err != nil {
			return err
		}
		task.Spec.Suspended = false
		if cleanBump != "" {
			existing, err := strconv.ParseFloat(normalizeBudgetBumpString(task.Spec.BudgetUSD), 64)
			if err == nil && !math.IsNaN(existing) && !math.IsInf(existing, 0) {
				task.Spec.BudgetUSD = fmt.Sprintf("%.2f", existing+bumpUSD)
			} else {
				task.Spec.BudgetUSD = fmt.Sprintf("%.2f", bumpUSD)
			}
		}
		if msg.BudgetTokensBump > 0 {
			task.Spec.BudgetTokens += msg.BudgetTokensBump
		}
		if task.Spec.MaxIterations > 0 && task.Status.Iteration >= task.Spec.MaxIterations {
			task.Spec.MaxIterations = task.Status.Iteration + 5
		}
		if err := s.client.Update(ctx, task); err != nil {
			return err
		}
		updatedTask = task
		return nil
	})
	if err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	if s.auditLogger != nil {
		s.auditLogger.LogMutation(ctx, "resource.resumed", "agenttask", msg.Name, namespace)
	}

	return connect.NewResponse(&pb.ResumeTaskResponse{
		Task: toProtoAgentTask(updatedTask),
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
	if err := ValidateDNS1123Label(msg.Name, "name"); err != nil {
		return nil, err
	}

	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "update", namespace, "agenttasks"); err != nil {
		return nil, err
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		task := &v1alpha1.AgentTask{}
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: msg.Name}, task); err != nil {
			return err
		}
		if task.Status.Phase == v1alpha1.AgentTaskPhaseCompleted || task.Status.Phase == v1alpha1.AgentTaskPhaseFailed {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("cannot guide task in terminal phase %s", task.Status.Phase))
		}
		if task.Annotations == nil {
			task.Annotations = make(map[string]string)
		}
		task.Annotations["divergedev.com/guidance"] = msg.Message
		task.Annotations["divergedev.com/guidance-timestamp"] = time.Now().UTC().Format(time.RFC3339)

		return s.client.Update(ctx, task)
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, err
		}
		return nil, SanitizeK8sError(s.logger, err)
	}

	if s.auditLogger != nil {
		s.auditLogger.LogMutation(ctx, "resource.guided", "agenttask", msg.Name, namespace)
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
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return err
	}
	if err := ValidateDNS1123Label(msg.Name, "name"); err != nil {
		return err
	}

	// RBAC: Authorize agenttask access AND pod logs access BEFORE reading resource to prevent information disclosure (CWE-200)
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "get", namespace, "agenttasks"); err != nil {
		return err
	}
	if err := AuthorizeTaskObjectAccess(ctx, s.auditLogger, "get", namespace, msg.Name); err != nil {
		return err
	}
	if err := AuthorizePodLogs(ctx, s.k8sClient, s.auditLogger, namespace); err != nil {
		return err
	}

	task := &v1alpha1.AgentTask{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: msg.Name}, task); err != nil {
		return SanitizeK8sError(s.logger, err)
	}

	podName := task.Status.SandboxPodName
	if podName == "" {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("sandbox pod not yet bound for task %s", msg.Name))
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

// toProtoAgentTask converts a Kubernetes v1alpha1.AgentTask resource into its protobuf representation.
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
