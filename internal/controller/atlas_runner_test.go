package controller

import (
	"context"
	"k8s.io/apimachinery/pkg/runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	r := &PreviewGroupReconciler{Client: c}
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
	r := &PreviewGroupReconciler{Client: c}
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
	r := &PreviewGroupReconciler{Client: c}

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
