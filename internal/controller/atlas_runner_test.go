package controller

import (
	"context"
	"k8s.io/apimachinery/pkg/runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/database"
	"hegel.dev/go/hegel"
)

func TestEnsureAtlasCR_Migration(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	c := fake.NewClientBuilder().WithScheme(s).Build()
	r := &EnvironmentReconciler{Client: c}
	ctx := context.Background()

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
					MigrationConfigMap: "my-migrations",
					Blocking:           &tFalse,
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{
		DSN: "postgres://user:pass@host/db",
	}

	err := r.ensureAtlasCR(ctx, env, dbResult)
	require.NoError(t, err)

	crName := generateHookJobName(env.Name, "atlas")

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("db.atlasgo.io/v1alpha1")
	u.SetKind("AtlasMigration")

	err = c.Get(ctx, client.ObjectKey{Name: crName, Namespace: "default"}, u)
	require.NoError(t, err)

	// Validate owner ref
	require.Len(t, u.GetOwnerReferences(), 1)
	assert.Equal(t, "test-env", u.GetOwnerReferences()[0].Name)

	// Validate urlFrom
	secretName, found, err := unstructured.NestedString(u.Object, "spec", "urlFrom", "secretKeyRef", "name")
	require.NoError(t, err)
	require.True(t, found)
	assert.Contains(t, secretName, "diverge-db-test-env")

	// Validate dir
	cmName, found, err := unstructured.NestedString(u.Object, "spec", "dir", "configMapRef", "name")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "my-migrations", cmName)
}

func TestEnsureAtlasCR_Schema(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
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
				Atlas: &divergeiov1alpha1.AtlasSpec{
					Mode:            "declarative",
					SchemaConfigMap: "my-schema",
					Blocking:        &tFalse,
					Policy: &divergeiov1alpha1.AtlasPolicySpec{
						Destructive: "error",
					},
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{
		DSN: "postgres://user:pass@host/db",
	}

	err := r.ensureAtlasCR(ctx, env, dbResult)
	require.NoError(t, err)

	crName := generateHookJobName(env.Name, "atlas")

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("db.atlasgo.io/v1alpha1")
	u.SetKind("AtlasSchema")

	err = c.Get(ctx, client.ObjectKey{Name: crName, Namespace: "default"}, u)
	require.NoError(t, err)

	// Validate schema
	cmName, found, err := unstructured.NestedString(u.Object, "spec", "schema", "configMapRef", "name")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "my-schema", cmName)

	// Validate policy
	policyDestructive, found, err := unstructured.NestedString(u.Object, "spec", "policy", "destructive")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "error", policyDestructive)
}

func TestEnsureAtlasCR_PBT(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	c := fake.NewClientBuilder().WithScheme(s).Build()
	ctx := context.Background()
	r := &EnvironmentReconciler{Client: c}

	hegel.Test(t, func(ht *hegel.T) {
		mode := hegel.Draw(ht, hegel.Text())
		cm := hegel.Draw(ht, hegel.Text())

		env := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-env",
				Namespace: "default",
			},
			Spec: divergeiov1alpha1.EnvironmentSpec{
				Database: divergeiov1alpha1.EnvironmentDatabase{
					Atlas: &divergeiov1alpha1.AtlasSpec{
						Mode:               mode,
						MigrationConfigMap: cm,
						SchemaConfigMap:    cm,
					},
				},
			},
		}

		dbResult := &database.DatabaseResult{
			DSN: "postgres://user:pass@host/db",
		}

		err := r.ensureAtlasCR(ctx, env, dbResult)
		assert.Error(t, err)

		crName := generateHookJobName(env.Name, "atlas")
		kind := "AtlasMigration"
		if mode == "declarative" {
			kind = "AtlasSchema"
		}

		u := &unstructured.Unstructured{}
		u.SetAPIVersion("db.atlasgo.io/v1alpha1")
		u.SetKind(kind)

		err2 := c.Get(ctx, client.ObjectKey{Name: crName, Namespace: "default"}, u)
		assert.NoError(t, err2)

		err3 := c.Delete(ctx, u)
		require.NoError(t, err3)
	})
}

func TestEnsureAtlasJob_Versioned(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-migrations",
			Namespace:       "default",
			ResourceVersion: "100",
		},
		Data: map[string]string{
			"atlas.sum":         "h1:test",
			"20240101_init.sql": "CREATE TABLE users (id serial primary key);",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(cm).Build()
	r := &EnvironmentReconciler{Client: c}
	ctx := context.Background()

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
					MigrationConfigMap: "my-migrations",
					Blocking:           &tFalse,
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{
		DSN: "postgres://user:pass@host/db?search_path=preview_test_env,public",
	}

	err := r.ensureAtlasJob(ctx, env, dbResult)
	require.NoError(t, err)

	var jobList batchv1.JobList
	err = c.List(ctx, &jobList, client.InNamespace("default"))
	require.NoError(t, err)
	require.Len(t, jobList.Items, 1)

	job := jobList.Items[0]
	assert.Contains(t, job.Name, "hook-test-env-atlas-")
	assert.Equal(t, "arigaio/atlas:latest", job.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, []string{"migrate", "apply", "--url", "$(DATABASE_URL)", "--dir", "file:///migrations"}, job.Spec.Template.Spec.Containers[0].Args)

	// Check labels
	assert.Equal(t, "migration", job.Labels["divergedev.com/hook-type"])
	assert.Equal(t, "test-env", job.Labels["divergedev.com/environment"])
	assert.Equal(t, "versioned", job.Labels["divergedev.com/atlas-mode"])

	// Check volume & volume mount
	require.Len(t, job.Spec.Template.Spec.Volumes, 2) // tmp + atlas-migrations
	assert.Equal(t, "atlas-migrations", job.Spec.Template.Spec.Volumes[1].Name)
	assert.Equal(t, "my-migrations", job.Spec.Template.Spec.Volumes[1].ConfigMap.Name)

	require.Len(t, job.Spec.Template.Spec.Containers[0].VolumeMounts, 2) // tmp + atlas-migrations
	assert.Equal(t, "/migrations", job.Spec.Template.Spec.Containers[0].VolumeMounts[1].MountPath)
	assert.True(t, job.Spec.Template.Spec.Containers[0].VolumeMounts[1].ReadOnly)

	// Check environment variables: HOME=/tmp and DATABASE_URL
	var foundHome, foundDBURL bool
	for _, ev := range job.Spec.Template.Spec.Containers[0].Env {
		if ev.Name == "HOME" && ev.Value == "/tmp" {
			foundHome = true
		}
		if ev.Name == "DATABASE_URL" && ev.ValueFrom != nil && ev.ValueFrom.SecretKeyRef != nil {
			foundDBURL = true
			assert.Contains(t, ev.ValueFrom.SecretKeyRef.Name, "diverge-db-test-env")
			assert.Equal(t, "url", ev.ValueFrom.SecretKeyRef.Key)
		}
	}
	assert.True(t, foundHome, "HOME=/tmp env var must be set for writable cache")
	assert.True(t, foundDBURL, "DATABASE_URL secret ref must be set")
}

func TestEnsureAtlasJob_Declarative(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-schema",
			Namespace:       "default",
			ResourceVersion: "42",
		},
		Data: map[string]string{
			"schema.sql": "CREATE TABLE posts (id serial primary key, title text);",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(cm).Build()
	r := &EnvironmentReconciler{Client: c}
	ctx := context.Background()

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
					Mode:            "declarative",
					Engine:          "job",
					Image:           "custom-registry/atlas:v0.20",
					SchemaConfigMap: "my-schema",
					Blocking:        &tFalse,
					Policy: &divergeiov1alpha1.AtlasPolicySpec{
						Destructive: "allow",
					},
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{
		DSN: "postgres://user:pass@host/db?search_path=preview_test_env,public",
	}

	err := r.ensureAtlasJob(ctx, env, dbResult)
	require.NoError(t, err)

	var jobList batchv1.JobList
	err = c.List(ctx, &jobList, client.InNamespace("default"))
	require.NoError(t, err)
	require.Len(t, jobList.Items, 1)

	job := jobList.Items[0]
	assert.Equal(t, "custom-registry/atlas:v0.20", job.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, []string{
		"schema", "apply", "--url", "$(DATABASE_URL)", "--to", "file:///schema/schema.sql", "--auto-approve", "--allow-destructive",
	}, job.Spec.Template.Spec.Containers[0].Args)

	// Check volume & volume mount
	assert.Equal(t, "atlas-schema", job.Spec.Template.Spec.Volumes[1].Name)
	assert.Equal(t, "my-schema", job.Spec.Template.Spec.Volumes[1].ConfigMap.Name)
	assert.Equal(t, "/schema", job.Spec.Template.Spec.Containers[0].VolumeMounts[1].MountPath)
}

func TestEnsureAtlasJob_ResourceVersionHashChange(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-migrations",
			Namespace:       "default",
			ResourceVersion: "1",
		},
		Data: map[string]string{
			"atlas.sum": "h1:v1",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(cm).Build()
	r := &EnvironmentReconciler{Client: c}
	ctx := context.Background()

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
					MigrationConfigMap: "my-migrations",
					Blocking:           &tFalse,
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{
		DSN: "postgres://user:pass@host/db",
	}

	// First execution
	err := r.ensureAtlasJob(ctx, env, dbResult)
	require.NoError(t, err)

	var jobList1 batchv1.JobList
	err = c.List(ctx, &jobList1, client.InNamespace("default"))
	require.NoError(t, err)
	require.Len(t, jobList1.Items, 1)
	jobName1 := jobList1.Items[0].Name

	// Update ConfigMap (simulating new migration commit)
	var latestCM corev1.ConfigMap
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "my-migrations", Namespace: "default"}, &latestCM))
	latestCM.Data["atlas.sum"] = "h1:v2"
	require.NoError(t, c.Update(ctx, &latestCM))

	// Second execution should create a new Job with different hash
	err = r.ensureAtlasJob(ctx, env, dbResult)
	require.NoError(t, err)

	var jobList2 batchv1.JobList
	err = c.List(ctx, &jobList2, client.InNamespace("default"))
	require.NoError(t, err)
	require.Len(t, jobList2.Items, 2)

	var foundNewJob bool
	for _, j := range jobList2.Items {
		if j.Name != jobName1 {
			foundNewJob = true
		}
	}
	assert.True(t, foundNewJob, "New Job must be created when ConfigMap ResourceVersion changes")
}

func TestEnsureAtlasJob_Status(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-migrations",
			Namespace:       "default",
			ResourceVersion: "1",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(cm).Build()
	r := &EnvironmentReconciler{Client: c}
	ctx := context.Background()

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
					MigrationConfigMap: "my-migrations",
					Blocking:           &tTrue,
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{
		DSN: "postgres://user:pass@host/db",
	}

	// 1. Initial run creates job and returns ErrHookInProgress
	err := r.ensureAtlasJob(ctx, env, dbResult)
	require.ErrorIs(t, err, ErrHookInProgress)

	// Get created job
	var jobList batchv1.JobList
	require.NoError(t, c.List(ctx, &jobList, client.InNamespace("default")))
	require.Len(t, jobList.Items, 1)
	job := &jobList.Items[0]

	// 2. Mark complete
	job.Status.Conditions = []batchv1.JobCondition{
		{
			Type:   batchv1.JobComplete,
			Status: corev1.ConditionTrue,
		},
	}
	require.NoError(t, c.Status().Update(ctx, job))

	err = r.ensureAtlasJob(ctx, env, dbResult)
	assert.NoError(t, err)

	// 3. Mark failed
	job.Status.Conditions = []batchv1.JobCondition{
		{
			Type:   batchv1.JobFailed,
			Status: corev1.ConditionTrue,
			Reason: "BackoffLimitExceeded",
		},
	}
	require.NoError(t, c.Status().Update(ctx, job))

	err = r.ensureAtlasJob(ctx, env, dbResult)
	assert.ErrorIs(t, err, ErrHookFailed)
}

func TestRunMigrations_AtlasDispatch(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-migrations",
			Namespace:       "default",
			ResourceVersion: "1",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(cm).Build()
	r := &EnvironmentReconciler{Client: c}
	ctx := context.Background()

	tFalse := false
	dbResult := &database.DatabaseResult{DSN: "postgres://user:pass@host/db"}

	// When Engine == "job", should create a batchv1.Job
	envJob := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "env-job", Namespace: "default", UID: "uid-job"},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Atlas: &divergeiov1alpha1.AtlasSpec{
					Mode:               "versioned",
					Engine:             "job",
					MigrationConfigMap: "my-migrations",
					Blocking:           &tFalse,
				},
			},
		},
	}
	require.NoError(t, r.runMigrations(ctx, envJob, dbResult))

	var jobList batchv1.JobList
	require.NoError(t, c.List(ctx, &jobList, client.InNamespace("default")))
	assert.Len(t, jobList.Items, 1)

	// When Engine == "operator" (or default), should create Atlas CR
	envOperator := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "env-op", Namespace: "default", UID: "uid-op"},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Atlas: &divergeiov1alpha1.AtlasSpec{
					Mode:               "versioned",
					Engine:             "operator",
					MigrationConfigMap: "my-migrations",
					Blocking:           &tFalse,
				},
			},
		},
	}
	require.NoError(t, r.runMigrations(ctx, envOperator, dbResult))

	crName := generateHookJobName("env-op", "atlas")
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("db.atlasgo.io/v1alpha1")
	u.SetKind("AtlasMigration")
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: crName, Namespace: "default"}, u))
}

func TestEnsureAtlasJob_ExtraArgs(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-migrations",
			Namespace:       "default",
			ResourceVersion: "1",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(cm).Build()
	r := &EnvironmentReconciler{Client: c}
	ctx := context.Background()

	tFalse := false
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-extra",
			Namespace: "default",
			UID:       "uid-extra",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Atlas: &divergeiov1alpha1.AtlasSpec{
					Mode:               "versioned",
					Engine:             "job",
					MigrationConfigMap: "my-migrations",
					Blocking:           &tFalse,
					ExtraArgs:          []string{"--log", "{{ .Files }}", "--var", "tenant=preview"},
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{DSN: "postgres://user:pass@host/db"}
	require.NoError(t, r.ensureAtlasJob(ctx, env, dbResult))

	var jobList batchv1.JobList
	require.NoError(t, c.List(ctx, &jobList, client.InNamespace("default")))
	require.Len(t, jobList.Items, 1)

	job1 := jobList.Items[0]
	expectedArgs := []string{
		"migrate", "apply", "--url", "$(DATABASE_URL)", "--dir", "file:///migrations",
		"--log", "{{ .Files }}", "--var", "tenant=preview",
	}
	assert.Equal(t, expectedArgs, job1.Spec.Template.Spec.Containers[0].Args)

	// Changing ExtraArgs generates a different job name hash
	env.Spec.Database.Atlas.ExtraArgs = []string{"--log", "json"}
	require.NoError(t, r.ensureAtlasJob(ctx, env, dbResult))

	require.NoError(t, c.List(ctx, &jobList, client.InNamespace("default")))
	require.Len(t, jobList.Items, 2)
	var foundNewJob bool
	for _, j := range jobList.Items {
		if j.Name != job1.Name {
			foundNewJob = true
		}
	}
	assert.True(t, foundNewJob, "New Job must be created when ExtraArgs change")
}

func TestEnsureAtlas_OwnerReference(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))

	// ConfigMap managed by Diverge CLI
	managedCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "atlas-managed-mig-12345678",
			Namespace:       "default",
			ResourceVersion: "1",
			Labels: map[string]string{
				"divergedev.com/managed-by":  "diverge",
				"divergedev.com/environment": "test-env",
			},
		},
	}

	// External unmanaged ConfigMap
	unmanagedCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "external-migrations",
			Namespace:       "default",
			ResourceVersion: "1",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(managedCM, unmanagedCM).Build()
	r := &EnvironmentReconciler{Client: c, Scheme: s}
	ctx := context.Background()

	tFalse := false
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
			UID:       "uid-12345",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Atlas: &divergeiov1alpha1.AtlasSpec{
					Mode:               "versioned",
					Engine:             "job",
					MigrationConfigMap: "atlas-managed-mig-12345678",
					Blocking:           &tFalse,
				},
			},
		},
	}

	dbResult := &database.DatabaseResult{DSN: "postgres://user:pass@host/db"}
	require.NoError(t, r.ensureAtlasJob(ctx, env, dbResult))

	var checkManaged corev1.ConfigMap
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "atlas-managed-mig-12345678", Namespace: "default"}, &checkManaged))
	require.Len(t, checkManaged.OwnerReferences, 1)
	assert.Equal(t, "test-env", checkManaged.OwnerReferences[0].Name)
	assert.Equal(t, "Environment", checkManaged.OwnerReferences[0].Kind)

	// Unmanaged should NOT get OwnerReference
	env.Spec.Database.Atlas.MigrationConfigMap = "external-migrations"
	require.NoError(t, r.ensureAtlasJob(ctx, env, dbResult))

	var checkUnmanaged corev1.ConfigMap
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "external-migrations", Namespace: "default"}, &checkUnmanaged))
	assert.Empty(t, checkUnmanaged.OwnerReferences)
}

func TestEnsureAtlasCR_StatusConditions(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	c := fake.NewClientBuilder().WithScheme(s).Build()
	r := &EnvironmentReconciler{Client: c, Scheme: s}
	ctx := context.Background()

	tTrue := true
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cr-status",
			Namespace: "default",
			UID:       "uid-status",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Atlas: &divergeiov1alpha1.AtlasSpec{
					Mode:               "versioned",
					Engine:             "operator",
					MigrationConfigMap: "some-cm",
					Blocking:           &tTrue,
				},
			},
		},
	}
	dbResult := &database.DatabaseResult{DSN: "postgres://user:pass@host/db"}

	// 1. Initial run creates CR and returns ErrHookInProgress
	err := r.ensureAtlasCR(ctx, env, dbResult)
	assert.ErrorIs(t, err, ErrHookInProgress)

	crName := generateHookJobName("test-cr-status", "atlas")
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("db.atlasgo.io/v1alpha1")
	u.SetKind("AtlasMigration")
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: crName, Namespace: "default"}, u))

	// 2. Set condition Ready: True -> returns nil
	conditions := []interface{}{
		map[string]interface{}{
			"type":   "Ready",
			"status": "True",
		},
	}
	require.NoError(t, unstructured.SetNestedSlice(u.Object, conditions, "status", "conditions"))
	require.NoError(t, c.Update(ctx, u))

	assert.NoError(t, r.ensureAtlasCR(ctx, env, dbResult))

	// 3. Set condition Ready: False with Reason: Failed -> returns ErrHookFailed
	failConditions := []interface{}{
		map[string]interface{}{
			"type":   "Ready",
			"status": "False",
			"reason": "Failed",
		},
	}
	require.NoError(t, unstructured.SetNestedSlice(u.Object, failConditions, "status", "conditions"))
	require.NoError(t, c.Update(ctx, u))

	assert.ErrorIs(t, r.ensureAtlasCR(ctx, env, dbResult), ErrHookFailed)
}
