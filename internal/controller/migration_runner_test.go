package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"k8s.io/apimachinery/pkg/runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/database"
)

func TestRunMigrationJob_Success(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	c := fake.NewClientBuilder().WithScheme(s).Build()
	r := &EnvironmentReconciler{Client: c}

	ctx := context.Background()

	tFalse := false
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				MigrationJob: &divergeiov1alpha1.MigrationJobSpec{
					Image:    "alpine",
					Blocking: &tFalse,
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{
		DSN: "postgres://user:pass@host/db",
	}

	err := r.runMigrationJob(ctx, env, dbResult)
	require.NoError(t, err)

	hashInput := "alpine"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]
	jobName := generateHookJobName(env.Name, "migration-"+hash)

	// Assert Job was created
	var job batchv1.Job
	err = c.Get(ctx, client.ObjectKey{Name: jobName, Namespace: "default"}, &job)
	require.NoError(t, err)

	// Assert Secret was created
	var secretList corev1.SecretList
	err = c.List(ctx, &secretList, client.InNamespace("default"))
	require.NoError(t, err)
	require.Len(t, secretList.Items, 1)
	secret := secretList.Items[0]

	// Assert DATABASE_URL injected
	c0 := job.Spec.Template.Spec.Containers[0]
	found := false
	for _, envVar := range c0.Env {
		if envVar.Name == "DATABASE_URL" {
			found = true
			require.NotNil(t, envVar.ValueFrom)
			require.NotNil(t, envVar.ValueFrom.SecretKeyRef)
			assert.Equal(t, secret.Name, envVar.ValueFrom.SecretKeyRef.Name)
		}
	}
	assert.True(t, found, "DATABASE_URL environment variable not found")
}

func TestRunMigrationJob_BlockingTrue_Wait(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	c := fake.NewClientBuilder().WithScheme(s).Build()
	r := &EnvironmentReconciler{Client: c}

	ctx := context.Background()

	tTrue := true
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				MigrationJob: &divergeiov1alpha1.MigrationJobSpec{
					Image:    "alpine",
					Blocking: &tTrue,
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{
		DSN: "postgres://user:pass@host/db",
	}

	err := r.runMigrationJob(ctx, env, dbResult)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is still running")

	// Set Job to complete
	hashInput := "alpine"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]
	jobName := generateHookJobName(env.Name, "migration-"+hash)
	var job batchv1.Job
	err = c.Get(ctx, client.ObjectKey{Name: jobName, Namespace: "default"}, &job)
	require.NoError(t, err)
	job.Status.Conditions = []batchv1.JobCondition{
		{
			Type:   batchv1.JobComplete,
			Status: corev1.ConditionTrue,
		},
	}
	err = c.Status().Update(ctx, &job)
	require.NoError(t, err)

	// Call again
	err = r.runMigrationJob(ctx, env, dbResult)
	require.NoError(t, err) // Should succeed now
}

func TestRunMigrationJob_BlockingTrue_Failed(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	c := fake.NewClientBuilder().WithScheme(s).Build()
	r := &EnvironmentReconciler{Client: c}

	ctx := context.Background()

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				MigrationJob: &divergeiov1alpha1.MigrationJobSpec{
					Image: "alpine",
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{
		DSN: "postgres://user:pass@host/db",
	}

	_ = r.runMigrationJob(ctx, env, dbResult)

	// Set Job to failed
	hashInput := "alpine"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]
	jobName := generateHookJobName(env.Name, "migration-"+hash)
	var job batchv1.Job
	err := c.Get(ctx, client.ObjectKey{Name: jobName, Namespace: "default"}, &job)
	require.NoError(t, err)
	job.Status.Conditions = []batchv1.JobCondition{
		{
			Type:   batchv1.JobFailed,
			Status: corev1.ConditionTrue,
			Reason: "OOMKilled",
		},
	}
	err = c.Status().Update(ctx, &job)
	require.NoError(t, err)

	// Call again
	err = r.runMigrationJob(ctx, env, dbResult)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed: OOMKilled")
}
