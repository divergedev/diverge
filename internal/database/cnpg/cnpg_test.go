package cnpg

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, v1alpha1.AddToScheme(s))
	return s
}

func TestClusterName(t *testing.T) {
	assert.Equal(t, "diverge-pg-pr-42", ClusterName("PR_42"))
	longEnv := strings.Repeat("feature-branch-", 5)
	cn := ClusterName(longEnv)
	assert.LessOrEqual(t, len(cn), 50)
	assert.Equal(t, cn, ClusterName(longEnv), "ClusterName must be deterministic")
}

func TestCNPGDatabaseProvider_Provision_CloneFromSeedSource(t *testing.T) {
	scheme := newTestScheme(t)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	prov := &CNPGDatabaseProvider{
		Client:      k8sClient,
		Scheme:      scheme,
		StorageSize: "2Gi",
		Instances:   1,
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-10",
			Namespace: "default",
			UID:       "uid-10",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Database: v1alpha1.EnvironmentDatabase{
				Mode:       "snapshot",
				SeedSource: "main-db",
			},
		},
	}

	res, err := prov.Provision(context.Background(), env)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.Ready, "newly created cluster should not be ready until status is healthy")
	assert.Equal(t, "postgres://app@diverge-pg-pr-10-rw.default.svc:5432/app?sslmode=disable", res.DSN)
	assert.Equal(t, "diverge-pg-pr-10", res.EnvVars["DIVERGE_CNPG_CLUSTER"])

	// Verify Unstructured Cluster spec
	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(ClusterGVK)
	require.NoError(t, k8sClient.Get(context.Background(), types.NamespacedName{
		Name:      "diverge-pg-pr-10",
		Namespace: "default",
	}, cluster))

	source, found, err := unstructured.NestedString(cluster.Object, "spec", "bootstrap", "pg_basebackup", "source")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "main-db", source)

	extClusters, found, err := unstructured.NestedSlice(cluster.Object, "spec", "externalClusters")
	require.NoError(t, err)
	assert.True(t, found)
	require.Len(t, extClusters, 1)
}

func TestCNPGDatabaseProvider_Provision_FreshInitDBAndSecretURI(t *testing.T) {
	scheme := newTestScheme(t)
	appSec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-pg-pr-fresh-app",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"uri": []byte("postgres://app:generated-pw@diverge-pg-pr-fresh-rw.default.svc:5432/app"),
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(appSec).Build()

	prov := &CNPGDatabaseProvider{
		Client:        k8sClient,
		Scheme:        scheme,
		SourceCluster: "main-db", // should be ignored when mode == "fresh"
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-fresh",
			Namespace: "default",
			UID:       "uid-fresh",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Database: v1alpha1.EnvironmentDatabase{
				Mode: "fresh",
			},
		},
	}

	res, err := prov.Provision(context.Background(), env)
	require.NoError(t, err)
	assert.Equal(t, "postgres://app:generated-pw@diverge-pg-pr-fresh-rw.default.svc:5432/app", res.DSN)

	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(ClusterGVK)
	require.NoError(t, k8sClient.Get(context.Background(), types.NamespacedName{
		Name:      "diverge-pg-pr-fresh",
		Namespace: "default",
	}, cluster))

	dbName, found, err := unstructured.NestedString(cluster.Object, "spec", "bootstrap", "initdb", "database")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "app", dbName)
}

func TestCNPGDatabaseProvider_StatusAndTeardown(t *testing.T) {
	scheme := newTestScheme(t)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	prov := &CNPGDatabaseProvider{
		Client: k8sClient,
		Scheme: scheme,
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-status",
			Namespace: "default",
			UID:       "uid-status",
		},
	}

	// Provision initial cluster
	_, err := prov.Provision(context.Background(), env)
	require.NoError(t, err)

	// Mark cluster healthy
	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(ClusterGVK)
	require.NoError(t, k8sClient.Get(context.Background(), types.NamespacedName{
		Name:      "diverge-pg-pr-status",
		Namespace: "default",
	}, cluster))
	require.NoError(t, unstructured.SetNestedField(cluster.Object, "Cluster in healthy state", "status", "phase"))
	require.NoError(t, k8sClient.Update(context.Background(), cluster))

	st, err := prov.Status(context.Background(), env)
	require.NoError(t, err)
	assert.True(t, st.Provisioned)
	assert.Contains(t, st.Message, "Cluster in healthy state")

	// Provision again when healthy returns Ready: true
	res, err := prov.Provision(context.Background(), env)
	require.NoError(t, err)
	assert.True(t, res.Ready)

	// Teardown deletes the cluster and is idempotent
	require.NoError(t, prov.Teardown(context.Background(), env))
	require.NoError(t, prov.Teardown(context.Background(), env))

	stAfter, err := prov.Status(context.Background(), env)
	require.NoError(t, err)
	assert.False(t, stAfter.Provisioned)
}
