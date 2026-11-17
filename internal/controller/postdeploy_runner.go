package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/multicluster"
)

func (r *PreviewGroupReconciler) resolveChildTargetCluster(ctx context.Context, env *divergeiov1alpha1.Environment) (client.Client, bool, error) {
	if !multicluster.IsRemoteEnv(env) {
		return r.Client, false, nil
	}
	resolver := r.ClusterClients
	if resolver == nil && r.Client != nil {
		resolver = multicluster.NewManager(r.Client, nil, nil, r.Scheme)
	}
	if resolver == nil {
		return nil, false, fmt.Errorf("multi-cluster resolver is not configured")
	}
	target, err := resolver.Resolve(ctx, env)
	if err != nil {
		return nil, false, err
	}
	return target.Client, target.IsRemote, nil
}

// runPostDeployJob creates and monitors a K8s Job for post-deploy hooks on the child's target cluster.
func (r *PreviewGroupReconciler) runPostDeployJob(ctx context.Context, env *divergeiov1alpha1.Environment, spec *divergeiov1alpha1.PostDeploySpec) error {
	logger := log.FromContext(ctx)

	targetClient, isRemote, err := r.resolveChildTargetCluster(ctx, env)
	if err != nil {
		return fmt.Errorf("failed to resolve target cluster for post-deploy job: %w", err)
	}

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	hashInput := spec.Image + strings.Join(spec.Args, "")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]
	jobName := generateHookJobName(env.Name, "postdeploy-"+hash)

	timeout := defaultPostDeployTimeout
	if spec.TimeoutSeconds > 0 {
		timeout = spec.TimeoutSeconds
	}

	cfg := HookJobConfig{
		JobName:      jobName,
		Namespace:    targetNS,
		Image:        spec.Image,
		Args:         spec.Args,
		Timeout:      timeout,
		Owner:        env,
		SkipOwnerRef: isRemote || targetNS != env.Namespace,
		Labels: map[string]string{
			labelHookType:    hookTypePostDeploy,
			labelEnvironment: env.Name,
			labelManagedBy:   "diverge",
		},
	}

	for _, ref := range spec.EnvFrom {
		cfg.EnvVars = append(cfg.EnvVars, corev1.EnvVar{
			Name: ref.Key,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: ref.Name,
					},
					Key: ref.Key,
				},
			},
		})
	}

	job := buildJob(cfg)

	// Create or get existing Job on the resolved target cluster
	var existingJob batchv1.Job
	err = targetClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: targetNS}, &existingJob)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get post-deploy job: %w", err)
		}

		logger.Info("Creating post-deploy job", "jobName", jobName, "namespace", targetNS, "remoteCluster", isRemote)
		if err := targetClient.Create(ctx, job); err != nil {
			return fmt.Errorf("failed to create post-deploy job: %w", err)
		}
	} else {
		job = &existingJob
	}

	isBlocking := false
	if spec.Blocking != nil {
		isBlocking = *spec.Blocking
	}

	if !isBlocking {
		return nil
	}

	// Check job status (blocking)
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			logger.Info("Post-deploy job completed successfully", "jobName", jobName)
			return nil
		}
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return fmt.Errorf("post-deploy job %s failed: %s: %w", jobName, cond.Reason, ErrHookFailed)
		}
	}

	return fmt.Errorf("post-deploy job %s is still running: %w", jobName, ErrHookInProgress)
}

func (r *PreviewGroupReconciler) cleanupPostDeployJobs(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	if env == nil {
		return nil
	}
	targetClient, _, err := r.resolveChildTargetCluster(ctx, env)
	if err != nil {
		if _, isVal := multicluster.IsValidationError(err); isVal && !multicluster.HasConfirmedRemoteTarget(env) {
			return nil
		}
		return fmt.Errorf("failed to resolve target cluster for post-deploy job cleanup: %w", err)
	}
	if targetClient == nil {
		return nil
	}

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	selector := labels.SelectorFromSet(map[string]string{
		labelEnvironment: truncateLabel(env.Name),
		labelHookType:    hookTypePostDeploy,
	})
	var jobList batchv1.JobList
	if err := targetClient.List(ctx, &jobList, client.InNamespace(targetNS), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			return nil
		}
		return fmt.Errorf("failed to list post-deploy jobs for cleanup: %w", err)
	}
	delOpts := []client.DeleteOption{
		client.PropagationPolicy(metav1.DeletePropagationBackground),
	}
	for i := range jobList.Items {
		if err := targetClient.Delete(ctx, &jobList.Items[i], delOpts...); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete post-deploy job %s/%s: %w", targetNS, jobList.Items[i].Name, err)
		}
	}
	return nil
}
