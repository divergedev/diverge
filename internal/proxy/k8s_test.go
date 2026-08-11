package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func getTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	return s
}

func TestK8sEnvironmentLister_Fallback(t *testing.T) {
	ctx := context.Background()
	s := getTestScheme()
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Status: v1alpha1.EnvironmentStatus{
			Phase: v1alpha1.PhaseRunning,
		},
	}
	client := fake.NewClientBuilder().WithScheme(s).WithObjects(env).Build()

	lister := &K8sEnvironmentLister{
		client:      client,
		namespace:   "default",
		useFallback: true,
		ttlCache:    make(map[string]*ttlEntry),
	}

	assert.True(t, lister.HasSynced())

	// Test GetEnvironment
	info, err := lister.GetEnvironment(ctx, "test-env")
	require.NoError(t, err)
	assert.Equal(t, "test-env", info.Name)
	assert.Equal(t, string(v1alpha1.PhaseRunning), info.Phase)

	// Test cache hit
	require.NoError(t, client.Delete(ctx, env)) // delete from fake client
	info, err = lister.GetEnvironment(ctx, "test-env")
	require.NoError(t, err)
	assert.Equal(t, "test-env", info.Name) // still cached

	// Test cache expiry (simulate)
	lister.ttlMu.Lock()
	lister.ttlCache["test-env"].expiresAt = time.Now().Add(-1 * time.Second)
	lister.ttlMu.Unlock()

	_, err = lister.GetEnvironment(ctx, "test-env")
	assert.ErrorContains(t, err, "not found")

	// Test ListEnvironments
	newEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env2",
			Namespace: "default",
		},
	}
	require.NoError(t, client.Create(ctx, newEnv))
	envs, err := lister.ListEnvironments(ctx)
	require.NoError(t, err)
	assert.Len(t, envs, 1)
	assert.Equal(t, "test-env2", envs[0].Name)
}

func TestK8sEnvironmentLister_Informer(t *testing.T) {
	ctx := context.Background()
	lister := &K8sEnvironmentLister{
		useFallback: false,
		envCache: map[string]EnvironmentInfo{
			"test-env": {Name: "test-env", Phase: "Running"},
		},
		hasSynced: func() bool { return true },
	}

	assert.True(t, lister.HasSynced())

	info, err := lister.GetEnvironment(ctx, "test-env")
	require.NoError(t, err)
	assert.Equal(t, "test-env", info.Name)

	_, err = lister.GetEnvironment(ctx, "non-existent")
	assert.ErrorContains(t, err, "environment not found")

	envs, err := lister.ListEnvironments(ctx)
	require.NoError(t, err)
	assert.Len(t, envs, 1)

	// Test not synced
	lister.hasSynced = func() bool { return false }
	assert.False(t, lister.HasSynced())
	_, err = lister.GetEnvironment(ctx, "test-env")
	assert.ErrorIs(t, err, ErrCacheNotSynced)

	_, err = lister.ListEnvironments(ctx)
	assert.ErrorIs(t, err, ErrCacheNotSynced)
}
