package server

import (
	"context"
	"errors"
	"sort"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
)

// k8sCallTimeout bounds all Kubernetes API calls made by hook RPCs
// to prevent indefinite blocking when the API server is unresponsive.
const k8sCallTimeout = 30 * time.Second

// ListHookJobs returns hook Jobs for an environment, sorted newest-first,
// with each Job's K8s status mapped to a proto phase (Pending/Running/Succeeded/Failed).
func (s *EnvironmentService) ListHookJobs(ctx context.Context, req *connect.Request[pb.ListHookJobsRequest]) (*connect.Response[pb.ListHookJobsResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, k8sCallTimeout)
	defer cancel()

	namespace := req.Msg.Namespace
	if namespace == "" {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}
	if err := ValidateDNS1123Label(req.Msg.EnvironmentName, "environment_name"); err != nil {
		return nil, err
	}

	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "list", namespace, "jobs"); err != nil {
		return nil, err
	}

	var jobList batchv1.JobList
	selector := labels.Set{"diverge.io/environment": req.Msg.EnvironmentName}.AsSelector()
	if err := s.client.List(ctx, &jobList, client.InNamespace(namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	var jobs []*pb.HookJob
	for _, job := range jobList.Items {
		phase := "Pending"
		var message string

		if len(job.Status.Conditions) > 0 {
			for _, cond := range job.Status.Conditions {
				if cond.Type == batchv1.JobComplete && cond.Status == "True" {
					phase = "Succeeded"
					message = cond.Message
					break
				}
				if cond.Type == batchv1.JobFailed && cond.Status == "True" {
					phase = "Failed"
					message = cond.Message
					break
				}
			}
		} else if job.Status.Active > 0 {
			phase = "Running"
		}

		pbJob := &pb.HookJob{
			Name:      job.Name,
			Type:      job.Labels["diverge.io/hook-type"],
			Phase:     phase,
			Message:   message,
			CreatedAt: timestamppb.New(job.CreationTimestamp.Time),
		}

		if job.Status.CompletionTime != nil {
			pbJob.CompletedAt = timestamppb.New(job.Status.CompletionTime.Time)
			pbJob.DurationSeconds = int32(job.Status.CompletionTime.Time.Sub(job.CreationTimestamp.Time).Seconds())
		}

		jobs = append(jobs, pbJob)
	}

	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.AsTime().After(jobs[j].CreatedAt.AsTime())
	})

	return connect.NewResponse(&pb.ListHookJobsResponse{Jobs: jobs}), nil
}

// RetryHook deletes the newest failed Job matching the hook type, then annotates
// the Environment CR with a retry marker so the controller reconciles a new run.
// It does NOT accept arbitrary image/command — only re-triggers from CRD spec.
func (s *EnvironmentService) RetryHook(ctx context.Context, req *connect.Request[pb.RetryHookRequest]) (*connect.Response[pb.RetryHookResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, k8sCallTimeout)
	defer cancel()

	namespace := req.Msg.Namespace
	if namespace == "" {
		namespace = "default"
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return nil, err
	}
	if err := ValidateDNS1123Label(req.Msg.EnvironmentName, "environment_name"); err != nil {
		return nil, err
	}
	if req.Msg.HookType != "migration" && req.Msg.HookType != "postdeploy" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("hook_type must be migration or postdeploy"))
	}

	// Authorize both the job deletion and the environment annotation mutation.
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "create", namespace, "jobs"); err != nil {
		return nil, err
	}
	if err := AuthorizeAction(ctx, s.k8sClient, s.auditLogger, "update", namespace, "environments"); err != nil {
		return nil, err
	}

	var jobList batchv1.JobList
	selector := labels.Set{
		"diverge.io/environment": req.Msg.EnvironmentName,
		"diverge.io/hook-type":   req.Msg.HookType,
	}.AsSelector()
	if err := s.client.List(ctx, &jobList, client.InNamespace(namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	var failedJob *batchv1.Job
	for i, job := range jobList.Items {
		isFailed := false
		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobFailed && cond.Status == "True" {
				isFailed = true
				break
			}
		}
		if isFailed {
			if failedJob == nil || job.CreationTimestamp.After(failedJob.CreationTimestamp.Time) {
				failedJob = &jobList.Items[i]
			}
		}
	}

	if failedJob == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no failed job found to retry"))
	}

	// Validate the Environment exists BEFORE deleting the Job to avoid
	// orphaned side-effects when the Environment has already been removed.
	var env v1alpha1.Environment
	if err := s.client.Get(ctx, client.ObjectKey{Name: req.Msg.EnvironmentName, Namespace: namespace}, &env); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	if err := s.client.Delete(ctx, failedJob, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	// Use Patch (merge) instead of read-modify-write to avoid 409 Conflict
	// races with the controller reconciling the same Environment concurrently.
	patch := client.MergeFrom(env.DeepCopy())
	if env.Annotations == nil {
		env.Annotations = make(map[string]string)
	}
	env.Annotations["diverge.io/retry-hook"] = req.Msg.HookType

	if err := s.client.Patch(ctx, &env, patch); err != nil {
		return nil, SanitizeK8sError(s.logger, err)
	}

	s.auditLogger.LogMutation(ctx, "retry", "hook-job", failedJob.Name, namespace)

	pendingJob := &pb.HookJob{
		Name:      failedJob.Name,
		Type:      req.Msg.HookType,
		Phase:     "Pending",
		CreatedAt: timestamppb.Now(),
	}

	return connect.NewResponse(&pb.RetryHookResponse{Job: pendingJob}), nil
}
