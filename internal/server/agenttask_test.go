package server

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	coretesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func buildTaskTestSetup() (*runtime.Scheme, *AgentTaskService) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	k8s := fakek8s.NewSimpleClientset()
	k8s.PrependReactor("create", "subjectaccessreviews", func(action coretesting.Action) (handled bool, ret runtime.Object, err error) {
		sar := action.(coretesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = true
		return true, sar, nil
	})

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	audit := NewAuditLogger(logger)

	svc := NewAgentTaskService(c, k8s, nil, nil, logger, audit)
	return scheme, svc
}

func TestAgentTaskServiceCRUD(t *testing.T) {
	ctx := context.Background()
	ctx = auth.ContextWithUserInfo(ctx, &auth.UserInfo{Username: "test-user"})
	_, svc := buildTaskTestSetup()

	// 1. CreateTask
	createReq := connect.NewRequest(&pb.CreateTaskRequest{
		Namespace: "default",
		Name:      "task-auth-fix",
		Spec: &pb.AgentTaskSpec{
			Objective:    "Fix auth race condition",
			BudgetUsd:    "5.00",
			BudgetTokens: 50000,
			Capabilities: []string{"gemini-2.5-pro"},
			Repository: &pb.AgentTaskRepository{
				Url:        "https://github.com/divergedev/diverge",
				BaseBranch: "main",
			},
			Sandbox: &pb.AgentTaskSandbox{
				Provider: "agent-sandbox",
			},
		},
	})

	createRes, err := svc.CreateTask(ctx, createReq)
	require.NoError(t, err)
	require.NotNil(t, createRes.Msg.Task)
	assert.Equal(t, "task-auth-fix", createRes.Msg.Task.Name)
	assert.Equal(t, "Fix auth race condition", createRes.Msg.Task.Spec.Objective)

	// 2. Duplicate CreateTask fails
	_, err = svc.CreateTask(ctx, createReq)
	assert.Error(t, err)
	var connectErr *connect.Error
	if assert.ErrorAs(t, err, &connectErr) {
		assert.Equal(t, connect.CodeAlreadyExists, connectErr.Code())
	}

	// 3. GetTask
	getRes, err := svc.GetTask(ctx, connect.NewRequest(&pb.GetTaskRequest{
		Namespace: "default",
		Name:      "task-auth-fix",
	}))
	require.NoError(t, err)
	assert.Equal(t, "task-auth-fix", getRes.Msg.Task.Name)

	// 4. ListTasks
	listRes, err := svc.ListTasks(ctx, connect.NewRequest(&pb.ListTasksRequest{
		Namespace: "default",
	}))
	require.NoError(t, err)
	assert.Len(t, listRes.Msg.Tasks, 1)

	// 5. GuideTask
	guideRes, err := svc.GuideTask(ctx, connect.NewRequest(&pb.GuideTaskRequest{
		Namespace: "default",
		Name:      "task-auth-fix",
		Message:   "Use sync.Map for caching",
	}))
	require.NoError(t, err)
	assert.True(t, guideRes.Msg.Accepted)

	// 6. PauseTask
	pauseRes, err := svc.PauseTask(ctx, connect.NewRequest(&pb.PauseTaskRequest{
		Namespace: "default",
		Name:      "task-auth-fix",
	}))
	require.NoError(t, err)
	assert.True(t, pauseRes.Msg.Task.Spec.Suspended)

	// 7. ResumeTask with budget bump (additive)
	resumeRes, err := svc.ResumeTask(ctx, connect.NewRequest(&pb.ResumeTaskRequest{
		Namespace:        "default",
		Name:             "task-auth-fix",
		BudgetUsdBump:    "10.00",
		BudgetTokensBump: 100000,
	}))
	require.NoError(t, err)
	assert.False(t, resumeRes.Msg.Task.Spec.Suspended)
	assert.Equal(t, "15.00", resumeRes.Msg.Task.Spec.BudgetUsd)
	assert.Equal(t, int64(150000), resumeRes.Msg.Task.Spec.BudgetTokens)

	// 8. DeleteTask
	_, err = svc.DeleteTask(ctx, connect.NewRequest(&pb.DeleteTaskRequest{
		Namespace: "default",
		Name:      "task-auth-fix",
	}))
	require.NoError(t, err)

	// 9. Get after delete returns CodeNotFound
	_, err = svc.GetTask(ctx, connect.NewRequest(&pb.GetTaskRequest{
		Namespace: "default",
		Name:      "task-auth-fix",
	}))
	assert.Error(t, err)
	if assert.ErrorAs(t, err, &connectErr) {
		assert.Equal(t, connect.CodeNotFound, connectErr.Code())
	}
}

func TestAgentTaskServiceValidation(t *testing.T) {
	ctx := context.Background()
	ctx = auth.ContextWithUserInfo(ctx, &auth.UserInfo{Username: "test-user"})
	_, svc := buildTaskTestSetup()

	// Missing name
	_, err := svc.CreateTask(ctx, connect.NewRequest(&pb.CreateTaskRequest{
		Namespace: "default",
		Name:      "",
	}))
	assert.Error(t, err)

	// Invalid DNS label name
	_, err = svc.CreateTask(ctx, connect.NewRequest(&pb.CreateTaskRequest{
		Namespace: "default",
		Name:      "Invalid_Uppercase_Name",
	}))
	assert.Error(t, err)

	// Missing Spec
	_, err = svc.CreateTask(ctx, connect.NewRequest(&pb.CreateTaskRequest{
		Namespace: "default",
		Name:      "task-no-spec",
		Spec:      nil,
	}))
	assert.Error(t, err)

	// Missing Objective
	_, err = svc.CreateTask(ctx, connect.NewRequest(&pb.CreateTaskRequest{
		Namespace: "default",
		Name:      "task-no-objective",
		Spec: &pb.AgentTaskSpec{
			Objective: "",
			Repository: &pb.AgentTaskRepository{
				Url: "https://github.com/org/repo",
			},
		},
	}))
	assert.Error(t, err)

	// Missing Repository URL
	_, err = svc.CreateTask(ctx, connect.NewRequest(&pb.CreateTaskRequest{
		Namespace: "default",
		Name:      "task-no-repo",
		Spec: &pb.AgentTaskSpec{
			Objective: "Some task",
			Repository: &pb.AgentTaskRepository{
				Url: "",
			},
		},
	}))
	assert.Error(t, err)

	// Invalid negative budget_usd
	_, err = svc.CreateTask(ctx, connect.NewRequest(&pb.CreateTaskRequest{
		Namespace: "default",
		Name:      "task-bad-budget",
		Spec: &pb.AgentTaskSpec{
			Objective: "Some task",
			BudgetUsd: "-5.00",
			Repository: &pb.AgentTaskRepository{
				Url: "https://github.com/org/repo",
			},
		},
	}))
	assert.Error(t, err)
}

func TestAgentTaskServiceListAllNamespacesAndBudgetValidation(t *testing.T) {
	ctx := context.Background()
	ctx = auth.ContextWithUserInfo(ctx, &auth.UserInfo{Username: "test-user"})
	_, svc := buildTaskTestSetup()

	for _, tc := range []struct {
		ns   string
		name string
	}{
		{ns: "default", name: "task-default"},
		{ns: "staging", name: "task-staging"},
	} {
		_, err := svc.CreateTask(ctx, connect.NewRequest(&pb.CreateTaskRequest{
			Namespace: tc.ns,
			Name:      tc.name,
			Spec: &pb.AgentTaskSpec{
				Objective: "Cross-namespace test",
				BudgetUsd: "10.00",
				Repository: &pb.AgentTaskRepository{
					Url: "https://github.com/divergedev/diverge",
				},
			},
		}))
		require.NoError(t, err)
	}

	// Empty namespace lists tasks across all namespaces (used by Web UI /tasks page)
	allRes, err := svc.ListTasks(ctx, connect.NewRequest(&pb.ListTasksRequest{
		Namespace:  "",
		Pagination: &pb.PaginationRequest{PageSize: 50},
	}))
	require.NoError(t, err)
	assert.Len(t, allRes.Msg.Tasks, 2)
	assert.NotNil(t, allRes.Msg.Pagination)

	// Specific namespace filters to only that namespace
	stagingRes, err := svc.ListTasks(ctx, connect.NewRequest(&pb.ListTasksRequest{
		Namespace: "staging",
	}))
	require.NoError(t, err)
	require.Len(t, stagingRes.Msg.Tasks, 1)
	assert.Equal(t, "task-staging", stagingRes.Msg.Tasks[0].Name)

	// ResumeTask rejects negative or non-numeric budgetUsdBump
	for _, badBump := range []string{"-10.00", "0", "not-a-number"} {
		_, err = svc.ResumeTask(ctx, connect.NewRequest(&pb.ResumeTaskRequest{
			Namespace:     "default",
			Name:          "task-default",
			BudgetUsdBump: badBump,
		}))
		require.Error(t, err, "expected error for budgetUsdBump=%q", badBump)
		var connectErr *connect.Error
		if assert.ErrorAs(t, err, &connectErr) {
			assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
		}
	}
}

func TestAgentTaskMacaroonCapabilityAuthorization(t *testing.T) {
	_, svc := buildTaskTestSetup()

	// Seed a task in "default" and "other-ns" as a normal user
	adminCtx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "admin"})
	for _, ns := range []string{"default", "other-ns"} {
		_, err := svc.CreateTask(adminCtx, connect.NewRequest(&pb.CreateTaskRequest{
			Namespace: ns,
			Name:      "task-1",
			Spec: &pb.AgentTaskSpec{
				Objective: "Test macaroon authz",
				Repository: &pb.AgentTaskRepository{
					Url: "https://github.com/divergedev/diverge",
				},
			},
		}))
		require.NoError(t, err)
	}

	// Macaroon user bound to "default" with read-only tools ("diverge_get_*", "diverge_list_*")
	macaroonCtx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{
		Username: "system:agenttask:default:task-1",
		Groups:   []string{"system:authenticated", "diverge:agenttasks"},
		Extra: map[string]authorizationv1.ExtraValue{
			auth.ExtraKeyAuthType:     {"macaroon"},
			auth.ExtraKeyTaskID:       {"task-1"},
			auth.ExtraKeyNamespace:    {"default"},
			auth.ExtraKeyAllowedTools: {"diverge_get_*", "diverge_list_*"},
		},
	})

	// 1. Allowed tool in bound namespace ("default") succeeds
	getRes, err := svc.GetTask(macaroonCtx, connect.NewRequest(&pb.GetTaskRequest{
		Namespace: "default",
		Name:      "task-1",
	}))
	require.NoError(t, err)
	assert.Equal(t, "task-1", getRes.Msg.Task.Name)

	// 2. Allowed tool in different namespace ("other-ns") is denied
	_, err = svc.GetTask(macaroonCtx, connect.NewRequest(&pb.GetTaskRequest{
		Namespace: "other-ns",
		Name:      "task-1",
	}))
	require.Error(t, err)
	var connectErr *connect.Error
	if assert.ErrorAs(t, err, &connectErr) {
		assert.Equal(t, connect.CodePermissionDenied, connectErr.Code())
	}

	// 3. Cluster-wide list (empty namespace) is denied for namespace-bound Macaroon
	_, err = svc.ListTasks(macaroonCtx, connect.NewRequest(&pb.ListTasksRequest{
		Namespace: "",
	}))
	require.Error(t, err)
	if assert.ErrorAs(t, err, &connectErr) {
		assert.Equal(t, connect.CodePermissionDenied, connectErr.Code())
	}

	// 4. Disallowed tool (DeleteTask -> diverge_delete_task) in bound namespace is denied
	_, err = svc.DeleteTask(macaroonCtx, connect.NewRequest(&pb.DeleteTaskRequest{
		Namespace: "default",
		Name:      "task-1",
	}))
	require.Error(t, err)
	if assert.ErrorAs(t, err, &connectErr) {
		assert.Equal(t, connect.CodePermissionDenied, connectErr.Code())
	}
}
