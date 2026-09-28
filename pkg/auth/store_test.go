package auth

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestInMemoryTokenStore(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryTokenStore()

	taskID := "task-abc"
	namespace := "default"
	tokenData := []byte("sample-macaroon-token")

	// 1. Not found initially
	_, err := store.GetToken(ctx, taskID, namespace)
	assert.ErrorIs(t, err, ErrTokenNotFound)
	assert.False(t, store.IsRevoked(ctx, taskID, namespace))

	// 2. Save and retrieve
	err = store.SaveToken(ctx, taskID, namespace, tokenData)
	require.NoError(t, err)

	retrieved, err := store.GetToken(ctx, taskID, namespace)
	require.NoError(t, err)
	assert.Equal(t, tokenData, retrieved)

	// 3. Concurrent access test
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.GetToken(ctx, taskID, namespace)
			_ = store.IsRevoked(ctx, taskID, namespace)
		}()
	}
	wg.Wait()

	// 4. Revocation
	err = store.RevokeToken(ctx, taskID, namespace)
	require.NoError(t, err)
	assert.True(t, store.IsRevoked(ctx, taskID, namespace))

	_, err = store.GetToken(ctx, taskID, namespace)
	assert.ErrorIs(t, err, ErrTokenRevoked)

	// 5. Deletion
	err = store.DeleteToken(ctx, taskID, namespace)
	require.NoError(t, err)
	_, err = store.GetToken(ctx, taskID, namespace)
	assert.ErrorIs(t, err, ErrTokenNotFound)
}

func TestKubernetesSecretTokenStore(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	store := NewKubernetesSecretTokenStore(client, scheme)

	taskID := "task-k8s"
	namespace := "diverge-ns"
	tokenData := []byte("secret-token-payload")

	// 1. Not found
	_, err := store.GetToken(ctx, taskID, namespace)
	assert.ErrorIs(t, err, ErrTokenNotFound)
	assert.False(t, store.IsRevoked(ctx, taskID, namespace))

	// 2. Save (create)
	err = store.SaveToken(ctx, taskID, namespace, tokenData)
	require.NoError(t, err)

	retrieved, err := store.GetToken(ctx, taskID, namespace)
	require.NoError(t, err)
	assert.Equal(t, tokenData, retrieved)

	// 3. Update existing
	newTokenData := []byte("updated-token-payload")
	err = store.SaveToken(ctx, taskID, namespace, newTokenData)
	require.NoError(t, err)

	retrieved, err = store.GetToken(ctx, taskID, namespace)
	require.NoError(t, err)
	assert.Equal(t, newTokenData, retrieved)

	// 4. Revoke
	err = store.RevokeToken(ctx, taskID, namespace)
	require.NoError(t, err)
	assert.True(t, store.IsRevoked(ctx, taskID, namespace))

	_, err = store.GetToken(ctx, taskID, namespace)
	assert.ErrorIs(t, err, ErrTokenRevoked)

	// 5. Delete
	err = store.DeleteToken(ctx, taskID, namespace)
	require.NoError(t, err)

	_, err = store.GetToken(ctx, taskID, namespace)
	assert.ErrorIs(t, err, ErrTokenNotFound)
}

func TestTokenAuditors(t *testing.T) {
	ctx := context.Background()
	event := AuthEvent{
		Type:      EventMint,
		TaskID:    "task-audit",
		Namespace: "default",
		Principal: "user-1",
		Success:   true,
		Timestamp: time.Now(),
	}

	noop := &NoopTokenAuditor{}
	err := noop.RecordEvent(ctx, event)
	assert.NoError(t, err)

	loggerAuditor := NewLoggerTokenAuditor(slog.Default())
	err = loggerAuditor.RecordEvent(ctx, event)
	assert.NoError(t, err)
}
