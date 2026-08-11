package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divergedev/diverge/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = batchv1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	_ = v1alpha1.AddToScheme(s)
	return s
}

func testEnvWithMigration(name string) *v1alpha1.Environment {
	return &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Database: v1alpha1.EnvironmentDatabase{
				Mode: "schema",
				MigrationJob: &v1alpha1.MigrationJobSpec{
					Image:          "arigaio/atlas:latest",
					Args:           []string{"migrate", "apply"},
					TimeoutSeconds: 60,
				},
			},
		},
	}
}

func TestMigrationRunner_RunOrCheck_CreatesJob(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	runner := &MigrationRunner{Client: client}
	env := testEnvWithMigration("test-env")

	completed, err := runner.RunOrCheck(context.Background(), env, "test-secret")
	assert.NoError(t, err)
	assert.False(t, completed)

	var job batchv1.Job
	err = client.Get(context.Background(), types.NamespacedName{Name: "diverge-migrate-test-env", Namespace: "default"}, &job)
	require.NoError(t, err)

	assert.Equal(t, "arigaio/atlas:latest", job.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, []string{"migrate", "apply"}, job.Spec.Template.Spec.Containers[0].Args)
	assert.Equal(t, "test-secret", job.Spec.Template.Spec.Containers[0].Env[0].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, int64(60), *job.Spec.ActiveDeadlineSeconds)
	assert.Contains(t, job.Labels, "diverge.io/environment")
	assert.Equal(t, "test-env", job.OwnerReferences[0].Name)
}

func TestMigrationRunner_RunOrCheck_JobCompleted(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-migrate-test-env",
			Namespace: "default",
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{
					Type:   batchv1.JobComplete,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}).Build()

	runner := &MigrationRunner{Client: client}
	env := testEnvWithMigration("test-env")

	completed, err := runner.RunOrCheck(context.Background(), env, "test-secret")
	assert.NoError(t, err)
	assert.True(t, completed)
}

func TestMigrationRunner_RunOrCheck_JobFailed(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-migrate-test-env",
			Namespace: "default",
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{
					Type:    batchv1.JobFailed,
					Status:  corev1.ConditionTrue,
					Message: "backoff limit exceeded",
				},
			},
		},
	}).Build()

	runner := &MigrationRunner{Client: client}
	env := testEnvWithMigration("test-env")

	completed, err := runner.RunOrCheck(context.Background(), env, "test-secret")
	assert.ErrorContains(t, err, "backoff limit exceeded")
	assert.False(t, completed)
}

func TestMigrationRunner_RunOrCheck_JobStillRunning(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-migrate-test-env",
			Namespace: "default",
		},
		Status: batchv1.JobStatus{
			Active: 1, // running, no conditions yet
		},
	}).Build()

	runner := &MigrationRunner{Client: client}
	env := testEnvWithMigration("test-env")

	completed, err := runner.RunOrCheck(context.Background(), env, "test-secret")
	assert.NoError(t, err)
	assert.False(t, completed)
}

func TestMigrationRunner_RunOrCheck_NoMigrationSpec(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	runner := &MigrationRunner{Client: client}
	env := testEnvWithMigration("test-env")
	env.Spec.Database.MigrationJob = nil

	completed, err := runner.RunOrCheck(context.Background(), env, "test-secret")
	assert.NoError(t, err)
	assert.True(t, completed)
}

func TestMigrationRunner_RunOrCheck_MissingImage(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	runner := &MigrationRunner{Client: client}
	env := testEnvWithMigration("test-env")
	env.Spec.Database.MigrationJob.Image = ""

	completed, err := runner.RunOrCheck(context.Background(), env, "test-secret")
	assert.ErrorContains(t, err, "migrationJob.image is required")
	assert.False(t, completed)
}

func TestMigrationRunner_Cleanup(t *testing.T) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-migrate-test-env",
			Namespace: "default",
		},
	}
	client := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(job).Build()
	runner := &MigrationRunner{Client: client}
	env := testEnvWithMigration("test-env")

	err := runner.Cleanup(context.Background(), env)
	assert.NoError(t, err)

	var found batchv1.Job
	err = client.Get(context.Background(), types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, &found)
	assert.ErrorContains(t, err, "not found")
}

func TestMigrationRunner_Cleanup_NotFound(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	runner := &MigrationRunner{Client: client}
	env := testEnvWithMigration("test-env")

	err := runner.Cleanup(context.Background(), env)
	assert.NoError(t, err)
}
