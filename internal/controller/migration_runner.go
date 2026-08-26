package controller

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/database"
)

var (
	// ErrHookFailed indicates a hook executed and failed.
	ErrHookFailed = errors.New("hook failed")
	// ErrHookInProgress indicates a hook is currently running.
	ErrHookInProgress = errors.New("hook in progress")
)

// runMigrations checks if a migration is needed and runs it.
func (r *PreviewGroupReconciler) runMigrations(ctx context.Context, env *divergeiov1alpha1.Environment, dbResult *database.DatabaseResult) error {
	if env.Spec.Database.Atlas != nil && env.Spec.Database.MigrationJob != nil {
		return fmt.Errorf("cannot configure both Atlas and MigrationJob for database hooks")
	}
	if env.Spec.Database.Atlas != nil {
		return r.ensureAtlasCR(ctx, env, dbResult)
	}
	if env.Spec.Database.MigrationJob != nil {
		return r.runMigrationJob(ctx, env, dbResult)
	}
	if dbResult != nil && dbResult.SetupSQL != "" {
		// Already handled by schemaprovider.Provision()
		return nil
	}
	return nil
}

// runMigrationJob creates and monitors a K8s Job for database migrations.
func (r *PreviewGroupReconciler) runMigrationJob(ctx context.Context, env *divergeiov1alpha1.Environment, dbResult *database.DatabaseResult) error {
	logger := log.FromContext(ctx)
	mjSpec := env.Spec.Database.MigrationJob

	if dbResult == nil || dbResult.DSN == "" {
		return fmt.Errorf("migration job requires a provisioned database with DSN")
	}

	// 1. Create DSN Secret
	secretName, err := createDSNSecret(ctx, r.Client, env.Name, env.Namespace, dbResult.DSN, env)
	if err != nil {
		return fmt.Errorf("failed to create DSN secret for migration: %w", err)
	}

	hashInput := mjSpec.Image + strings.Join(mjSpec.Args, "")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]
	jobName := generateHookJobName(env.Name, "migration-"+hash)

	timeout := defaultMigrationTimeout
	if mjSpec.TimeoutSeconds > 0 {
		timeout = mjSpec.TimeoutSeconds
	}

	cfg := HookJobConfig{
		JobName:   jobName,
		Namespace: env.Namespace,
		Image:     mjSpec.Image,
		Args:      mjSpec.Args,
		Timeout:   timeout,
		Owner:     env,
		Labels: map[string]string{
			labelHookType:    hookTypeMigration,
			labelEnvironment: env.Name,
		},
		EnvVars: []corev1.EnvVar{
			{
				Name: "DATABASE_URL",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: secretName,
						},
						Key: "url",
					},
				},
			},
		},
	}

	for _, ref := range mjSpec.EnvFrom {
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
	err = r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: env.Namespace}, &existingJob)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get migration job: %w", err)
		}

		logger.Info("Creating migration job", "jobName", jobName)
		if err := r.Create(ctx, job); err != nil {
			return fmt.Errorf("failed to create migration job: %w", err)
		}
		// Treat as blocking, we must wait below if blocking
	} else {
		job = &existingJob
	}

	isBlocking := true
	if mjSpec.Blocking != nil {
		isBlocking = *mjSpec.Blocking
	}

	if !isBlocking {
		return nil
	}

	// Check job status (blocking)
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			logger.Info("Migration job completed successfully", "jobName", jobName)
			return nil
		}
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return fmt.Errorf("migration job %s failed: %s: %w", jobName, cond.Reason, ErrHookFailed)
		}
	}

	// Not complete or failed yet
	return fmt.Errorf("migration job %s is still running: %w", jobName, ErrHookInProgress)
}
