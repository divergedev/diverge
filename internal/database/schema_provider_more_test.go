package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestSchemaProvider_Provision_WithRunner_Completed(t *testing.T) {
	ctx := context.Background()
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{Namespace: "create"},
			Database: v1alpha1.EnvironmentDatabase{
				MigrationJob: &v1alpha1.MigrationJobSpec{
					Image: "my-image",
				},
			},
		},
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-migrate-test-env",
			Namespace: env.PreviewNamespace(),
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			},
		},
	}
	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(job).Build()
	mockExec := new(mockSQLExecutor)

	provider := &SchemaProvider{
		Executor: mockExec,
		Client:   fakeClient,
		Runner:   &MigrationRunner{Client: fakeClient},
	}

	schemaName, _ := SchemaName(env)
	mockExec.On("ExecContext", ctx, "CREATE SCHEMA IF NOT EXISTS "+schemaName, mock.Anything).Return(nil)

	status, err := provider.Provision(ctx, env)
	require.NoError(t, err)
	assert.True(t, status.Ready)
}

func TestSchemaProvider_Provision_WithRunner_Running(t *testing.T) {
	ctx := context.Background()
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{Namespace: "create"},
			Database: v1alpha1.EnvironmentDatabase{
				MigrationJob: &v1alpha1.MigrationJobSpec{
					Image: "my-image",
				},
			},
		},
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-migrate-test-env",
			Namespace: env.PreviewNamespace(),
		},
		Status: batchv1.JobStatus{
			Active: 1,
		},
	}
	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(job).Build()
	mockExec := new(mockSQLExecutor)

	provider := &SchemaProvider{
		Executor: mockExec,
		Client:   fakeClient,
		Runner:   &MigrationRunner{Client: fakeClient},
	}

	schemaName, _ := SchemaName(env)
	mockExec.On("ExecContext", ctx, "CREATE SCHEMA IF NOT EXISTS "+schemaName, mock.Anything).Return(nil)

	status, err := provider.Provision(ctx, env)
	require.NoError(t, err)
	assert.False(t, status.Ready) // still running
}

func TestSchemaProvider_Provision_UpdatesSecret(t *testing.T) {
	ctx := context.Background()
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	schemaName, _ := SchemaName(env)
	secretName := SecretName(schemaName)

	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: "default",
		},
		Data: map[string][]byte{
			"DATABASE_URL": []byte("old-url"),
		},
	}
	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(existingSecret).Build()
	mockExec := new(mockSQLExecutor)

	provider := &SchemaProvider{
		Executor: mockExec,
		Client:   fakeClient,
		BaseURL:  "postgres://usr:pwd@new-host/db",
	}

	mockExec.On("ExecContext", ctx, "CREATE SCHEMA IF NOT EXISTS "+schemaName, mock.Anything).Return(nil)

	status, err := provider.Provision(ctx, env)
	require.NoError(t, err)
	assert.True(t, status.Ready)

	// Check updated
	var secret corev1.Secret
	err = fakeClient.Get(ctx, client.ObjectKey{Name: secretName, Namespace: "default"}, &secret)
	require.NoError(t, err)

	val := string(secret.Data["DATABASE_URL"])
	if len(secret.StringData) > 0 {
		val = secret.StringData["DATABASE_URL"]
	}
	assert.Contains(t, val, "new-host")
}

func TestSchemaProvider_Teardown_WithRunner(t *testing.T) {
	ctx := context.Background()
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-migrate-test-env",
			Namespace: "default",
		},
	}
	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(job).Build()
	mockExec := new(mockSQLExecutor)

	provider := &SchemaProvider{
		Executor: mockExec,
		Client:   fakeClient,
		Runner:   &MigrationRunner{Client: fakeClient},
	}

	schemaName, _ := SchemaName(env)
	mockExec.On("ExecContext", ctx, "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE", mock.Anything).Return(nil)

	err := provider.Teardown(ctx, env)
	require.NoError(t, err)
	mockExec.AssertExpectations(t)

	// verify job deleted
	err = fakeClient.Get(ctx, types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, &batchv1.Job{})
	assert.ErrorContains(t, err, "not found")
}
