package database

import (
	"context"
	"fmt"

	"github.com/divergedev/diverge/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MigrationRunner creates and monitors Kubernetes Jobs for database migrations.
type MigrationRunner struct {
	Client client.Client
}

// migrationJobName returns a deterministic name for the migration Job.
func migrationJobName(env *v1alpha1.Environment) string {
	return fmt.Sprintf("diverge-migrate-%s", env.Name)
}

// RunOrCheck creates a migration Job if it doesn't exist, or checks the status
// of an existing one. Returns:
//   - (true, nil) if the Job completed successfully
//   - (false, nil) if the Job is still running (caller should requeue)
//   - (false, err) if the Job failed or couldn't be created
func (r *MigrationRunner) RunOrCheck(ctx context.Context, env *v1alpha1.Environment, connectionSecret string) (bool, error) {
	if env.Spec.Database.MigrationJob == nil {
		// No migration configured — skip
		return true, nil
	}

	spec := env.Spec.Database.MigrationJob
	if spec.Image == "" {
		return false, fmt.Errorf("migrationJob.image is required")
	}

	namespace := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		namespace = env.PreviewNamespace()
	}

	jobName := migrationJobName(env)

	// Check if Job already exists
	var existing batchv1.Job
	err := r.Client.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &existing)
	if err == nil {
		// Job exists — check status
		return r.checkJobStatus(&existing)
	}
	if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("failed to get migration job: %w", err)
	}

	// Job doesn't exist — create it
	job := r.buildJob(env, spec, namespace, jobName, connectionSecret)
	if err := r.Client.Create(ctx, job); err != nil {
		return false, fmt.Errorf("failed to create migration job: %w", err)
	}

	return false, nil // Job just created, requeue to check later
}

// checkJobStatus returns (completed, error) for an existing Job.
func (r *MigrationRunner) checkJobStatus(job *batchv1.Job) (bool, error) {
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			return true, nil
		}
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return false, fmt.Errorf("migration job failed: %s", cond.Message)
		}
	}
	// Still running
	return false, nil
}

// buildJob constructs the batch/v1 Job for running migrations.
func (r *MigrationRunner) buildJob(
	env *v1alpha1.Environment,
	spec *v1alpha1.MigrationJobSpec,
	namespace, jobName, connectionSecret string,
) *batchv1.Job {
	backoffLimit := int32(2)
	ttlAfterFinished := int32(300)
	timeoutSeconds := int64(120)
	if spec.TimeoutSeconds > 0 {
		timeoutSeconds = int64(spec.TimeoutSeconds)
	}

	// Build envFrom refs
	var envFromRefs []corev1.EnvFromSource
	for _, ref := range spec.EnvFrom {
		envFromRefs = append(envFromRefs, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: ref.Name,
				},
			},
		})
	}

	// Inject DATABASE_URL from connection secret
	envVars := []corev1.EnvVar{
		{
			Name: "DATABASE_URL",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: connectionSecret,
					},
					Key: "DATABASE_URL",
				},
			},
		},
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
			Labels: map[string]string{
				"diverge.io/environment": env.Name,
				"diverge.io/managed-by":  "diverge",
				"diverge.io/component":   "migration",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "diverge.io/v1alpha1",
					Kind:       "Environment",
					Name:       env.Name,
					UID:        env.UID,
				},
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttlAfterFinished,
			ActiveDeadlineSeconds:   &timeoutSeconds,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"diverge.io/environment": env.Name,
						"diverge.io/component":   "migration",
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Containers: []corev1.Container{
						{
							Name:    "migrate",
							Image:   spec.Image,
							Args:    spec.Args,
							Env:     envVars,
							EnvFrom: envFromRefs,
						},
					},
				},
			},
		},
	}
}

// Cleanup deletes the migration Job for an environment.
func (r *MigrationRunner) Cleanup(ctx context.Context, env *v1alpha1.Environment) error {
	namespace := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		namespace = env.PreviewNamespace()
	}

	jobName := migrationJobName(env)
	propagation := metav1.DeletePropagationBackground
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
		},
	}
	if err := r.Client.Delete(ctx, job, &client.DeleteOptions{
		PropagationPolicy: &propagation,
	}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete migration job: %w", err)
	}
	return nil
}
