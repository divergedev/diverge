package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	labelHookType                  = "diverge.io/hook-type"
	labelEnvironment               = "diverge.io/environment"
	hookTypeMigration              = "migration"
	hookTypePostDeploy             = "postdeploy"
	defaultMigrationTimeout  int32 = 120
	defaultPostDeployTimeout int32 = 60
	jobTTLAfterFinished      int32 = 300 // 5 min cleanup
)

// HookJobConfig holds the parameters for creating a hook Job.
type HookJobConfig struct {
	JobName   string
	Namespace string
	Image     string
	Args      []string
	EnvVars   []corev1.EnvVar
	EnvFrom   []corev1.EnvFromSource
	Timeout   int32
	Owner     metav1.Object
	Labels    map[string]string
}

// buildJob creates a K8s Job spec from HookJobConfig.
func buildJob(cfg HookJobConfig) *batchv1.Job {
	var backoffLimit int32 = 0
	var ttl int32 = jobTTLAfterFinished
	var activeDeadline *int64
	if cfg.Timeout > 0 {
		t := int64(cfg.Timeout)
		activeDeadline = &t
	}

	labels := make(map[string]string)
	for k, v := range cfg.Labels {
		labels[k] = truncateLabel(v)
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.JobName,
			Namespace: cfg.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   activeDeadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "hook",
							Image:   cfg.Image,
							Args:    cfg.Args,
							Env:     cfg.EnvVars,
							EnvFrom: cfg.EnvFrom,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("256Mi"),
								},
							},
						},
					},
				},
			},
		},
	}

	if cfg.Owner != nil {
		// Just a simple owner ref, assuming no scheme needed here if we do it manually,
		// but controllerutil.SetControllerReference is better if scheme is provided.
		// Since we don't pass scheme to buildJob, we'll manually append an OwnerReference.
		t := true
		job.OwnerReferences = append(job.OwnerReferences, metav1.OwnerReference{
			APIVersion:         "diverge.io/v1alpha1", // Wait, need to know owner's APIVersion? We assume it's Environment.
			Kind:               "Environment",
			Name:               cfg.Owner.GetName(),
			UID:                cfg.Owner.GetUID(),
			Controller:         &t,
			BlockOwnerDeletion: &t,
		})
	}

	return job
}

// truncateLabel ensures label values do not exceed the 63 character limit.
func truncateLabel(val string) string {
	if len(val) > validation.LabelValueMaxLength {
		return val[:validation.LabelValueMaxLength]
	}
	return val
}

// createDSNSecret creates a short-lived Secret containing the database DSN.
func createDSNSecret(ctx context.Context, c client.Client, name, namespace, dsn string, owner metav1.Object) (string, error) {
	secretName := sanitizeK8sName(fmt.Sprintf("diverge-db-%s", name), dsn, 253)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				labelHookType:    hookTypeMigration,
				labelEnvironment: truncateLabel(name),
			},
		},
		Data: map[string][]byte{
			"url": []byte(dsn),
		},
	}

	// Assuming owner is an Environment
	t := true
	secret.OwnerReferences = append(secret.OwnerReferences, metav1.OwnerReference{
		APIVersion:         "diverge.io/v1alpha1",
		Kind:               "Environment",
		Name:               owner.GetName(),
		UID:                owner.GetUID(),
		Controller:         &t,
		BlockOwnerDeletion: &t,
	})

	err := c.Create(ctx, secret)
	if err != nil {
		if client.IgnoreAlreadyExists(err) != nil {
			return "", err
		}
	}
	return secretName, nil
}

// sanitizeK8sName limits name length and ensures DNS-1123 compliance.
func sanitizeK8sName(raw, hashInput string, maxLength int) string {
	raw = strings.ToLower(raw)
	raw = strings.NewReplacer(".", "-", "_", "-").Replace(raw)
	raw = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(raw, "")
	raw = regexp.MustCompile(`-{2,}`).ReplaceAllString(raw, "-")
	raw = strings.Trim(raw, "-")

	if raw == "" {
		raw = "resource"
	}

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]
	if len(raw) <= maxLength-9 {
		return raw + "-" + hash
	}
	return raw[:maxLength-9] + "-" + hash
}

// generateHookJobName creates a DNS-1123 compliant job name.
func generateHookJobName(name, suffix string) string {
	raw := fmt.Sprintf("hook-%s-%s", name, suffix)
	return sanitizeK8sName(raw, name+"/"+suffix, 63)
}
