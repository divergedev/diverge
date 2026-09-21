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

	// 7. ResumeTask with budget bump
	resumeRes, err := svc.ResumeTask(ctx, connect.NewRequest(&pb.ResumeTaskRequest{
		Namespace:        "default",
		Name:             "task-auth-fix",
		BudgetUsdBump:    "10.00",
		BudgetTokensBump: 100000,
	}))
	require.NoError(t, err)
	assert.False(t, resumeRes.Msg.Task.Spec.Suspended)
	assert.Equal(t, "10.00", resumeRes.Msg.Task.Spec.BudgetUsd)
	assert.Equal(t, int64(100000), resumeRes.Msg.Task.Spec.BudgetTokens)

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
}
