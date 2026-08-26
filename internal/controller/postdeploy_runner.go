package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

// runPostDeployJob creates and monitors a K8s Job for post-deploy hooks.
func (r *PreviewGroupReconciler) runPostDeployJob(ctx context.Context, env *divergeiov1alpha1.Environment, spec *divergeiov1alpha1.PostDeploySpec) error {
	logger := log.FromContext(ctx)

	hashInput := spec.Image + strings.Join(spec.Args, "")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]
	jobName := generateHookJobName(env.Name, "postdeploy-"+hash)

	timeout := defaultPostDeployTimeout
	if spec.TimeoutSeconds > 0 {
		timeout = spec.TimeoutSeconds
	}

	cfg := HookJobConfig{
		JobName:   jobName,
		Namespace: env.Namespace,
		Image:     spec.Image,
		Args:      spec.Args,
		Timeout:   timeout,
		Owner:     env,
		Labels: map[string]string{
			labelHookType:    hookTypePostDeploy,
			labelEnvironment: env.Name,
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

	// Create or get existing Job
	var existingJob batchv1.Job
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: env.Namespace}, &existingJob)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get post-deploy job: %w", err)
		}

		logger.Info("Creating post-deploy job", "jobName", jobName)
		if err := r.Create(ctx, job); err != nil {
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
