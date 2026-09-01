package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func setupSQLTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	return s
}

func TestRunSetupSQLJob_CreatesConfigMapAndJob(t *testing.T) {
	scheme := setupSQLTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &EnvironmentReconciler{Client: c}

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
	}

	err := r.runSetupSQLJob(context.Background(), env, "CREATE SCHEMA foo;", "postgres://admin:pass@localhost/db")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrHookInProgress)

	// Verify ConfigMap was created
	cmList := &corev1.ConfigMapList{}
	require.NoError(t, c.List(context.Background(), cmList))
	require.Len(t, cmList.Items, 1)
	assert.Equal(t, "CREATE SCHEMA foo;", cmList.Items[0].Data["setup.sql"])
	assert.Equal(t, hookTypeSetupSQL, cmList.Items[0].Labels[labelHookType])

	// Verify Job was created
	jobList := &batchv1.JobList{}
	require.NoError(t, c.List(context.Background(), jobList))
	require.Len(t, jobList.Items, 1)
	assert.Contains(t, jobList.Items[0].Name, "setup-sql-test-env")
	assert.Equal(t, defaultSetupJobImage, jobList.Items[0].Spec.Template.Spec.Containers[0].Image)

	// Verify SQL ConfigMap is mounted
	volumes := jobList.Items[0].Spec.Template.Spec.Volumes
	found := false
	for _, v := range volumes {
		if v.Name == "setup-sql" {
			found = true
			break
		}
	}
	assert.True(t, found, "setup-sql volume should be mounted")
}

func TestRunSetupSQLJob_CompletedJob(t *testing.T) {
	scheme := setupSQLTestScheme()

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
	}

	setupSQL := "CREATE SCHEMA foo;"
	jobName := sanitizeK8sName("setup-sql-test-env", "test-env", 63)

	completedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
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
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(completedJob).Build()
	r := &EnvironmentReconciler{Client: c}

	err := r.runSetupSQLJob(context.Background(), env, setupSQL, "postgres://admin:pass@localhost/db")
	assert.NoError(t, err)
}

func TestRunSetupSQLJob_FailedJob(t *testing.T) {
	scheme := setupSQLTestScheme()

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
	}

	setupSQL := "CREATE SCHEMA foo;"
	jobName := sanitizeK8sName("setup-sql-test-env", "test-env", 63)

	failedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: "default",
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{
					Type:    batchv1.JobFailed,
					Status:  corev1.ConditionTrue,
					Message: "psql: connection refused",
				},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(failedJob).Build()
	r := &EnvironmentReconciler{Client: c}

	err := r.runSetupSQLJob(context.Background(), env, setupSQL, "postgres://admin:pass@localhost/db")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed")
	assert.NotErrorIs(t, err, ErrHookInProgress)
}

func TestRunSetupSQLJob_EmptySQL(t *testing.T) {
	r := &EnvironmentReconciler{}
	env := &divergeiov1alpha1.Environment{}

	err := r.runSetupSQLJob(context.Background(), env, "", "postgres://admin:pass@localhost/db")
	assert.NoError(t, err)
}
