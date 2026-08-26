package controller

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"hegel.dev/go/hegel"
)

func TestBuildJob(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-env",
			UID:  "test-uid",
		},
	}
	cfg := HookJobConfig{
		JobName:   "test-job",
		Namespace: "default",
		Image:     "alpine:latest",
		Args:      []string{"echo", "hello"},
		Timeout:   120,
		Owner:     env,
		Labels: map[string]string{
			"test-label": "true",
		},
	}

	job := buildJob(cfg)

	assert.Equal(t, "test-job", job.Name)
	assert.Equal(t, "default", job.Namespace)
	assert.Equal(t, "true", job.Labels["test-label"])

	require.NotNil(t, job.Spec.BackoffLimit)
	assert.Equal(t, int32(0), *job.Spec.BackoffLimit)

	require.NotNil(t, job.Spec.TTLSecondsAfterFinished)
	assert.Equal(t, int32(300), *job.Spec.TTLSecondsAfterFinished)

	require.NotNil(t, job.Spec.ActiveDeadlineSeconds)
	assert.Equal(t, int64(120), *job.Spec.ActiveDeadlineSeconds)

	assert.Equal(t, corev1.RestartPolicyNever, job.Spec.Template.Spec.RestartPolicy)

	require.Len(t, job.Spec.Template.Spec.Containers, 1)
	c := job.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "alpine:latest", c.Image)
	assert.Equal(t, []string{"echo", "hello"}, c.Args)

	require.Len(t, job.OwnerReferences, 1)
	assert.Equal(t, "test-env", job.OwnerReferences[0].Name)
	assert.Equal(t, "diverge.io/v1alpha1", job.OwnerReferences[0].APIVersion)
}

func TestCreateDSNSecret(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	ctx := context.Background()

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-env",
			UID:  "test-uid",
		},
	}

	secretName, err := createDSNSecret(ctx, c, "test-env", "default", "postgres://user:pass@host/db", env)
	require.NoError(t, err)

	var secret corev1.Secret
	err = c.Get(ctx, client.ObjectKey{Name: secretName, Namespace: "default"}, &secret)
	require.NoError(t, err)

	assert.Equal(t, hookTypeMigration, secret.Labels[labelHookType])
	assert.Equal(t, "test-env", secret.Labels[labelEnvironment])
	assert.Equal(t, "postgres://user:pass@host/db", string(secret.Data["url"]))
	require.Len(t, secret.OwnerReferences, 1)
	assert.Equal(t, "test-env", secret.OwnerReferences[0].Name)
}

func TestCreateDSNSecret_Idempotent(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	ctx := context.Background()

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-env",
		},
	}

	name1, err := createDSNSecret(ctx, c, "test-env", "default", "url", env)
	require.NoError(t, err)

	name2, err := createDSNSecret(ctx, c, "test-env", "default", "url", env)
	require.NoError(t, err)

	assert.Equal(t, name1, name2)
}

func TestPBT_JobName(t *testing.T) {
	dnsRegex := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

	hegel.Test(t, func(ht *hegel.T) {
		name := hegel.Draw(ht, hegel.Text())
		suffix := hegel.Draw(ht, hegel.Text())

		jobName := generateHookJobName(name, suffix)
		assert.LessOrEqual(t, len(jobName), 63)
		assert.True(t, dnsRegex.MatchString(jobName), "Job name %q must be a valid DNS-1123 label", jobName)
	})
}

func TestPBT_DSNSecret(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	ctx := context.Background()
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-env",
			UID:  "test-uid",
		},
	}

	hegel.Test(t, func(ht *hegel.T) {
		dsn := hegel.Draw(ht, hegel.Text())
		secretName, err := createDSNSecret(ctx, c, "test-env", "default", dsn, env)
		assert.NoError(t, err)
		assert.NotEmpty(t, secretName)

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: "default",
			},
			StringData: map[string]string{
				"url": dsn,
			},
		}

		assert.Equal(t, dsn, secret.StringData["url"])
	})
}
