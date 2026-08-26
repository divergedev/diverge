package server

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server/auth"
)

func TestListHookJobs(t *testing.T) {
	scheme, c, k8s, logger := buildEnvTestSetup()
	_ = batchv1.AddToScheme(scheme)

	audit := NewAuditLogger(logger)
	svc := NewEnvironmentService(c, k8s, nil, nil, NewStreamLimiter(250, 20), logger, audit)

	ctx := context.Background()
	ctx = auth.ContextWithUserInfo(ctx, &auth.UserInfo{Username: "test"})

	// Setup jobs
	jobs := []batchv1.Job{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-pending",
				Namespace: "default",
				Labels: map[string]string{
					"diverge.io/environment": "test-env",
					"diverge.io/hook-type":   "migration",
				},
				CreationTimestamp: metav1.Time{Time: time.Now().Add(-10 * time.Minute)},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-running",
				Namespace: "default",
				Labels: map[string]string{
					"diverge.io/environment": "test-env",
					"diverge.io/hook-type":   "migration",
				},
				CreationTimestamp: metav1.Time{Time: time.Now().Add(-5 * time.Minute)},
			},
			Status: batchv1.JobStatus{
				Active: 1,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-succeeded",
				Namespace: "default",
				Labels: map[string]string{
					"diverge.io/environment": "test-env",
					"diverge.io/hook-type":   "postdeploy",
				},
				CreationTimestamp: metav1.Time{Time: time.Now().Add(-3 * time.Minute)},
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{Type: batchv1.JobComplete, Status: corev1.ConditionTrue, Message: "All done"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-failed",
				Namespace: "default",
				Labels: map[string]string{
					"diverge.io/environment": "test-env",
					"diverge.io/hook-type":   "migration",
				},
				CreationTimestamp: metav1.Time{Time: time.Now().Add(-1 * time.Minute)},
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Message: "Failed to run"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-other-env",
				Namespace: "default",
				Labels: map[string]string{
					"diverge.io/environment": "other-env",
					"diverge.io/hook-type":   "migration",
				},
			},
		},
	}

	for _, j := range jobs {
		err := c.Create(ctx, &j)
		require.NoError(t, err)

		// For the fake client, status subresource must be updated separately or it is updated when we use Create/Update on the Job itself in some versions.
		// Controller runtime fake client handles Status if we pass it, but sometimes we need to do Status().Update().
		// We'll update status just in case.
		j.Status = jobs[0].Status // wait, need to use correct status
	}
	// Redo with direct status patch:
	for _, j := range jobs {
		var created batchv1.Job
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: j.Name, Namespace: j.Namespace}, &created))
		created.Status = j.Status
		require.NoError(t, c.Status().Update(ctx, &created))
	}

	t.Run("list jobs with phase mapping", func(t *testing.T) {
		req := &pb.ListHookJobsRequest{
			Namespace:       "default",
			EnvironmentName: "test-env",
		}
		resp, err := svc.ListHookJobs(ctx, connect.NewRequest(req))
		require.NoError(t, err)

		// Should return 4 jobs, sorted by created_at descending (newest first)
		require.Len(t, resp.Msg.Jobs, 4)

		assert.Equal(t, "job-failed", resp.Msg.Jobs[0].Name)
		assert.Equal(t, "Failed", resp.Msg.Jobs[0].Phase)
		assert.Equal(t, "Failed to run", resp.Msg.Jobs[0].Message)

		assert.Equal(t, "job-succeeded", resp.Msg.Jobs[1].Name)
		assert.Equal(t, "Succeeded", resp.Msg.Jobs[1].Phase)
		assert.Equal(t, "postdeploy", resp.Msg.Jobs[1].Type)

		assert.Equal(t, "job-running", resp.Msg.Jobs[2].Name)
		assert.Equal(t, "Running", resp.Msg.Jobs[2].Phase)

		assert.Equal(t, "job-pending", resp.Msg.Jobs[3].Name)
		assert.Equal(t, "Pending", resp.Msg.Jobs[3].Phase)
	})

	t.Run("empty list", func(t *testing.T) {
		req := &pb.ListHookJobsRequest{
			Namespace:       "default",
			EnvironmentName: "non-existent",
		}
		resp, err := svc.ListHookJobs(ctx, connect.NewRequest(req))
		require.NoError(t, err)
		assert.Empty(t, resp.Msg.Jobs)
	})
}

func TestRetryHook(t *testing.T) {
	scheme, c, k8s, logger := buildEnvTestSetup()
	_ = batchv1.AddToScheme(scheme)

	audit := NewAuditLogger(logger)
	svc := NewEnvironmentService(c, k8s, nil, nil, NewStreamLimiter(250, 20), logger, audit)

	ctx := context.Background()
	ctx = auth.ContextWithUserInfo(ctx, &auth.UserInfo{Username: "test"})

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	require.NoError(t, c.Create(ctx, env))

	jobs := []batchv1.Job{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-failed-old",
				Namespace: "default",
				Labels: map[string]string{
					"diverge.io/environment": "test-env",
					"diverge.io/hook-type":   "migration",
				},
				CreationTimestamp: metav1.Time{Time: time.Now().Add(-10 * time.Minute)},
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-failed-new",
				Namespace: "default",
				Labels: map[string]string{
					"diverge.io/environment": "test-env",
					"diverge.io/hook-type":   "migration",
				},
				CreationTimestamp: metav1.Time{Time: time.Now().Add(-5 * time.Minute)},
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
				},
			},
		},
	}

	for _, j := range jobs {
		err := c.Create(ctx, &j)
		require.NoError(t, err)
		var created batchv1.Job
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: j.Name, Namespace: j.Namespace}, &created))
		created.Status = j.Status
		require.NoError(t, c.Status().Update(ctx, &created))
	}

	t.Run("invalid hook type", func(t *testing.T) {
		req := &pb.RetryHookRequest{
			Namespace:       "default",
			EnvironmentName: "test-env",
			HookType:        "invalid",
		}
		_, err := svc.RetryHook(ctx, connect.NewRequest(req))
		require.Error(t, err)
		var cErr *connect.Error
		require.ErrorAs(t, err, &cErr)
		assert.Equal(t, connect.CodeInvalidArgument, cErr.Code())
	})

	t.Run("no failed job exists", func(t *testing.T) {
		req := &pb.RetryHookRequest{
			Namespace:       "default",
			EnvironmentName: "test-env",
			HookType:        "postdeploy", // No postdeploy jobs created
		}
		_, err := svc.RetryHook(ctx, connect.NewRequest(req))
		require.Error(t, err)
		var cErr *connect.Error
		require.ErrorAs(t, err, &cErr)
		assert.Equal(t, connect.CodeFailedPrecondition, cErr.Code())
	})

	t.Run("successful retry deletes newest failed job and annotates env", func(t *testing.T) {
		req := &pb.RetryHookRequest{
			Namespace:       "default",
			EnvironmentName: "test-env",
			HookType:        "migration",
		}
		resp, err := svc.RetryHook(ctx, connect.NewRequest(req))
		require.NoError(t, err)

		assert.Equal(t, "job-failed-new", resp.Msg.Job.Name)
		assert.Equal(t, "Pending", resp.Msg.Job.Phase)

		// Check job is deleted
		var job batchv1.Job
		err = c.Get(ctx, client.ObjectKey{Name: "job-failed-new", Namespace: "default"}, &job)
		assert.Error(t, err)

		// Check env is annotated
		var updatedEnv v1alpha1.Environment
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "test-env", Namespace: "default"}, &updatedEnv))
		assert.Equal(t, "migration", updatedEnv.Annotations["diverge.io/retry-hook"])
	})
}
