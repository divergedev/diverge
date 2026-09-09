package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconcileProvisioning_DBFailure(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	r, _, _, _, db := newTestReconciler(t, env, nil, "")
	db.provisionErr = errors.New("db provision error")

	statusBase := env.DeepCopy()
	res, done, err := r.reconcileProvisioning(context.Background(), env, statusBase)
	assert.Empty(t, res)
	assert.True(t, done)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db provision error")
}

func TestReconcileProvisioning_RoutingFailure(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	r, _, _, rot, _ := newTestReconciler(t, env, nil, "")
	rot.reconcileErr = errors.New("routing error")

	statusBase := env.DeepCopy()
	res, done, err := r.reconcileProvisioning(context.Background(), env, statusBase)
	assert.Empty(t, res)
	assert.True(t, done)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "routing error")
}

func TestNotifyFailed_NilNotifier(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	r, _, _, _, _ := newTestReconciler(t, env, nil, "")

	// Should not panic when Notifier is nil
	assert.NotPanics(t, func() {
		r.notifyFailed(context.Background(), env, "test error")
	})
}

// A simple mock notifier to test the error path
type errorNotifier struct{}

func (e *errorNotifier) PostEnvironmentCreated(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	return nil
}
func (e *errorNotifier) PostEnvironmentReady(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	return nil
}
func (e *errorNotifier) PostEnvironmentFailed(ctx context.Context, env *divergeiov1alpha1.Environment, reason string) error {
	return errors.New("notify error")
}
func (e *errorNotifier) PostEnvironmentTeardown(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	return nil
}
func (e *errorNotifier) UpdateEnvironmentStatus(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	return nil
}

func TestNotifyFailed_ErrorHandled(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	r, _, _, _, _ := newTestReconciler(t, env, nil, "")
	r.Notifier = &errorNotifier{}

	assert.NotPanics(t, func() {
		r.notifyFailed(context.Background(), env, "test error")
	})
}

func TestCrossNamespaceSecretRef_Rejected(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			EnvFrom: []divergeiov1alpha1.SecretRef{
				{Namespace: "kube-system", Name: "secret-1"},
			},
		},
	}
	r, _, _, _, _ := newTestReconciler(t, env, nil, "")

	statusBase := env.DeepCopy()
	res, done, err := r.reconcileProvisioning(context.Background(), env, statusBase)
	assert.Empty(t, res)
	assert.True(t, done)
	require.NoError(t, err)

	cond := env.Status.Conditions[0]
	assert.Equal(t, "SecretRefValid", cond.Type)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "CrossNamespaceRef", cond.Reason)
}

func TestCrossNamespaceSecretRef_SameNamespaceAllowed(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			EnvFrom: []divergeiov1alpha1.SecretRef{
				{Namespace: "default", Name: "secret-1"},
				{Name: "secret-2"}, // implicit same namespace
			},
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				EnvFrom: []divergeiov1alpha1.SecretRef{
					{Namespace: "default", Name: "secret-3"},
				},
			},
		},
	}
	r, _, _, rot, db := newTestReconciler(t, env, nil, "")
	db.provisionErr = errors.New("stop here")
	rot.reconcileErr = errors.New("stop here")

	statusBase := env.DeepCopy()
	// This will fail later in reconcileProvisioning, but pass the SecretRef check
	_, _, _ = r.reconcileProvisioning(context.Background(), env, statusBase)

	for _, cond := range env.Status.Conditions {
		assert.NotEqual(t, "CrossNamespaceRef", cond.Reason)
	}
}

func TestReconcileProvisioning_AtlasMigration_InProgress_Blocks(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "atlas-cm",
			Namespace: "default",
		},
	}
	tTrue := true
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Atlas: &divergeiov1alpha1.AtlasSpec{
					Mode:               "versioned",
					Engine:             "job",
					MigrationConfigMap: "atlas-cm",
					Blocking:           &tTrue,
				},
			},
		},
	}
	dbResult := &database.DatabaseResult{
		Ready: true,
		DSN:   "postgres://user:pass@host/db",
	}
	r, c, _, _, _ := newTestReconciler(t, env, dbResult, "https://test.com")
	require.NoError(t, c.Create(context.Background(), cm))

	statusBase := env.DeepCopy()
	res, done, err := r.reconcileProvisioning(context.Background(), env, statusBase)
	assert.True(t, done, "reconcileProvisioning must return done=true when blocking migration is running")
	require.NoError(t, err)
	assert.Equal(t, 3*time.Second, res.RequeueAfter)
	assert.Equal(t, divergeiov1alpha1.PhaseMigrating, env.Status.Phase)
	assert.Equal(t, "Running", env.Status.MigrationStatus)

	var foundCond bool
	for _, cond := range env.Status.Conditions {
		if cond.Type == "MigrationReady" {
			foundCond = true
			assert.Equal(t, metav1.ConditionFalse, cond.Status)
			assert.Equal(t, "MigrationRunning", cond.Reason)
		}
	}
	assert.True(t, foundCond, "MigrationReady condition must be set")
}

func TestReconcileProvisioning_AtlasMigration_Failed_Blocks(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "atlas-cm",
			Namespace: "default",
		},
	}
	tTrue := true
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Atlas: &divergeiov1alpha1.AtlasSpec{
					Mode:               "versioned",
					Engine:             "job",
					MigrationConfigMap: "atlas-cm",
					Blocking:           &tTrue,
				},
			},
		},
	}
	dbResult := &database.DatabaseResult{
		Ready: true,
		DSN:   "postgres://user:pass@host/db",
	}
	r, c, _, _, _ := newTestReconciler(t, env, dbResult, "https://test.com")
	require.NoError(t, c.Create(context.Background(), cm))

	// First execution creates the Job
	statusBase := env.DeepCopy()
	_, _, _ = r.reconcileProvisioning(context.Background(), env, statusBase)

	// Fetch Job and mark Failed
	var jobList batchv1.JobList
	require.NoError(t, c.List(context.Background(), &jobList, client.InNamespace("default")))
	require.Len(t, jobList.Items, 1)
	job := &jobList.Items[0]
	job.Status.Conditions = []batchv1.JobCondition{
		{
			Type:   batchv1.JobFailed,
			Status: corev1.ConditionTrue,
			Reason: "MigrationError",
		},
	}
	require.NoError(t, c.Status().Update(context.Background(), job))

	// Second execution detects failure
	statusBase2 := env.DeepCopy()
	res, done, err := r.reconcileProvisioning(context.Background(), env, statusBase2)
	assert.True(t, done, "reconcileProvisioning must return done=true when blocking migration fails")
	require.Error(t, err)
	assert.Empty(t, res)
	assert.Equal(t, divergeiov1alpha1.PhaseFailed, env.Status.Phase)
	assert.Equal(t, "Failed", env.Status.MigrationStatus)

	var foundCond bool
	for _, cond := range env.Status.Conditions {
		if cond.Type == "MigrationReady" {
			foundCond = true
			assert.Equal(t, metav1.ConditionFalse, cond.Status)
			assert.Equal(t, "MigrationFailed", cond.Reason)
		}
	}
	assert.True(t, foundCond, "MigrationReady condition must be set to Failed")
}

func TestReconcileProvisioning_AtlasMigration_Succeeded(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "atlas-cm",
			Namespace: "default",
		},
	}
	tTrue := true
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Atlas: &divergeiov1alpha1.AtlasSpec{
					Mode:               "versioned",
					Engine:             "job",
					MigrationConfigMap: "atlas-cm",
					Blocking:           &tTrue,
				},
			},
		},
	}
	dbResult := &database.DatabaseResult{
		Ready: true,
		DSN:   "postgres://user:pass@host/db",
	}
	r, c, _, _, _ := newTestReconciler(t, env, dbResult, "https://test.com")
	require.NoError(t, c.Create(context.Background(), cm))

	// First execution creates the Job
	statusBase := env.DeepCopy()
	_, _, _ = r.reconcileProvisioning(context.Background(), env, statusBase)

	// Fetch Job and mark Complete
	var jobList batchv1.JobList
	require.NoError(t, c.List(context.Background(), &jobList, client.InNamespace("default")))
	require.Len(t, jobList.Items, 1)
	job := &jobList.Items[0]
	job.Status.Conditions = []batchv1.JobCondition{
		{
			Type:   batchv1.JobComplete,
			Status: corev1.ConditionTrue,
		},
	}
	require.NoError(t, c.Status().Update(context.Background(), job))

	// Next execution succeeds and allows provisioning to proceed
	statusBase2 := env.DeepCopy()
	_, done, err := r.reconcileProvisioning(context.Background(), env, statusBase2)
	assert.False(t, done, "reconcileProvisioning should proceed when migration succeeds")
	require.NoError(t, err)
	assert.Equal(t, "Succeeded", env.Status.MigrationStatus)

	var foundCond bool
	for _, cond := range env.Status.Conditions {
		if cond.Type == "MigrationReady" {
			foundCond = true
			assert.Equal(t, metav1.ConditionTrue, cond.Status)
			assert.Equal(t, "MigrationSucceeded", cond.Reason)
		}
	}
	assert.True(t, foundCond, "MigrationReady condition must be Succeeded")
}

func TestReconcileProvisioning_AtlasMigration_NonBlocking(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "atlas-cm",
			Namespace: "default",
		},
	}
	tFalse := false
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Atlas: &divergeiov1alpha1.AtlasSpec{
					Mode:               "versioned",
					Engine:             "job",
					MigrationConfigMap: "atlas-cm",
					Blocking:           &tFalse,
				},
			},
		},
	}
	dbResult := &database.DatabaseResult{
		Ready: true,
		DSN:   "postgres://user:pass@host/db",
	}
	r, c, _, _, _ := newTestReconciler(t, env, dbResult, "https://test.com")
	require.NoError(t, c.Create(context.Background(), cm))

	statusBase := env.DeepCopy()
	_, done, err := r.reconcileProvisioning(context.Background(), env, statusBase)
	assert.False(t, done, "reconcileProvisioning should NOT block when blocking is false")
	require.NoError(t, err)
}
