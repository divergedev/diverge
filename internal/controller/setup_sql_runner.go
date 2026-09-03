package controller

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

const (
	defaultSetupJobImage = "postgres:17-alpine@sha256:7456ef82e5f5bc43d997f4781bbd7c0d6389bff397564649a356e206ba473aee"
	hookTypeSetupSQL     = "setup-sql"
)

// runSetupSQLJob creates and monitors a K8s Job that executes SetupSQL
// via psql, isolating admin database operations from the controller process.
func (r *EnvironmentReconciler) runSetupSQLJob(ctx context.Context, env *divergeiov1alpha1.Environment, setupSQL, adminDSN string) error {
	logger := log.FromContext(ctx)

	if setupSQL == "" {
		return nil
	}

	// 1. Create ConfigMap with the SetupSQL content
	cmName := sanitizeK8sName(fmt.Sprintf("diverge-setup-sql-%s", env.Name), setupSQL, 253)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: env.Namespace,
			Labels: map[string]string{
				labelHookType:    hookTypeSetupSQL,
				labelEnvironment: truncateLabel(env.Name),
			},
		},
		Data: map[string]string{
			"setup.sql": setupSQL,
		},
	}

	t := true
	cm.OwnerReferences = append(cm.OwnerReferences, metav1.OwnerReference{
		APIVersion:         "diverge.io/v1alpha1",
		Kind:               "Environment",
		Name:               env.GetName(),
		UID:                env.GetUID(),
		Controller:         &t,
		BlockOwnerDeletion: &t,
	})

	if err := r.Create(ctx, cm); err != nil {
		if client.IgnoreAlreadyExists(err) != nil {
			return fmt.Errorf("failed to create setup-sql configmap: %w", err)
		}
	}

	// 2. Create admin DSN Secret
	secretName, err := createDSNSecret(ctx, r.Client, fmt.Sprintf("%s-admin", env.Name), env.Namespace, adminDSN, env)
	if err != nil {
		return fmt.Errorf("failed to create admin DSN secret for setup-sql: %w", err)
	}

	// 3. Build and create Job — derive name from env (stable), not SQL content
	// (which changes each requeue due to random password generation).
	jobName := sanitizeK8sName(fmt.Sprintf("setup-sql-%s", env.Name), env.Name, 63)

	existingJob := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: env.Namespace}, existingJob); err == nil {
		// Job already exists — check completion
		for _, cond := range existingJob.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				logger.Info("SetupSQL job completed successfully", "job", jobName)
				return nil
			}
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				return fmt.Errorf("setup-sql job %s failed: %s", jobName, cond.Message)
			}
		}
		logger.Info("SetupSQL job is still running", "job", jobName)
		return fmt.Errorf("setup-sql job %s is still running: %w", jobName, ErrHookInProgress)
	}

	image := r.SetupJobImage
	if image == "" {
		image = defaultSetupJobImage
	}

	job := buildJob(HookJobConfig{
		JobName:   jobName,
		Namespace: env.Namespace,
		Image:     image,
		Args:      []string{"psql", "-v", "ON_ERROR_STOP=1", "-d", "$(DATABASE_URL)", "-f", "/setup/setup.sql"},
		EnvVars: []corev1.EnvVar{
			{
				Name: "DATABASE_URL",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
						Key:                  "url",
					},
				},
			},
		},
		Timeout: 300,
		Owner:   env,
		Labels: map[string]string{
			labelHookType:    hookTypeSetupSQL,
			labelEnvironment: env.Name,
		},
	})

	// Add ConfigMap volume and mount
	job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: "setup-sql",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
			},
		},
	})
	job.Spec.Template.Spec.Containers[0].VolumeMounts = append(
		job.Spec.Template.Spec.Containers[0].VolumeMounts,
		corev1.VolumeMount{
			Name:      "setup-sql",
			MountPath: "/setup",
			ReadOnly:  true,
		},
	)

	// Set owner reference for GC
	job.OwnerReferences = append(job.OwnerReferences, metav1.OwnerReference{
		APIVersion:         "diverge.io/v1alpha1",
		Kind:               "Environment",
		Name:               env.GetName(),
		UID:                env.GetUID(),
		Controller:         &t,
		BlockOwnerDeletion: &t,
	})

	if err := r.Create(ctx, job); err != nil {
		if client.IgnoreAlreadyExists(err) != nil {
			return fmt.Errorf("failed to create setup-sql job: %w", err)
		}
	}

	logger.Info("Created SetupSQL job", "job", jobName, "configmap", cmName)
	return fmt.Errorf("setup-sql job %s created, waiting for completion: %w", jobName, ErrHookInProgress)
}
