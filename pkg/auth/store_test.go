package auth

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	indexStore := NewKubernetesSecretTokenStore(client, scheme).WithIndexOnly()

	taskID := "task-k8s"
	namespace := "diverge-ns"
	tokenData := []byte("secret-token-payload")

	// 1. Not found
	_, err := store.GetToken(ctx, taskID, namespace)
	assert.ErrorIs(t, err, ErrTokenNotFound)
	assert.False(t, store.IsRevoked(ctx, taskID, namespace))
	_, err = indexStore.GetToken(ctx, taskID, namespace)
	assert.ErrorIs(t, err, ErrTokenNotFound)
	assert.False(t, indexStore.IsRevoked(ctx, taskID, namespace))

	// 2. Save (create)
	err = store.SaveToken(ctx, taskID, namespace, tokenData)
	require.NoError(t, err)

	retrieved, err := store.GetToken(ctx, taskID, namespace)
	require.NoError(t, err)
	assert.Equal(t, tokenData, retrieved)
	retrievedIdx, err := indexStore.GetToken(ctx, taskID, namespace)
	require.NoError(t, err)
	assert.Equal(t, tokenData, retrievedIdx)

	// 3. Update existing
	newTokenData := []byte("updated-token-payload")
	err = store.SaveToken(ctx, taskID, namespace, newTokenData)
	require.NoError(t, err)

	retrieved, err = store.GetToken(ctx, taskID, namespace)
	require.NoError(t, err)
	assert.Equal(t, newTokenData, retrieved)
	retrievedIdx, err = indexStore.GetToken(ctx, taskID, namespace)
	require.NoError(t, err)
	assert.Equal(t, newTokenData, retrievedIdx)

	// 4. Revoke
	err = store.RevokeToken(ctx, taskID, namespace)
	require.NoError(t, err)
	assert.True(t, store.IsRevoked(ctx, taskID, namespace))
	assert.True(t, indexStore.IsRevoked(ctx, taskID, namespace))

	_, err = store.GetToken(ctx, taskID, namespace)
	assert.ErrorIs(t, err, ErrTokenRevoked)
	_, err = indexStore.GetToken(ctx, taskID, namespace)
	assert.ErrorIs(t, err, ErrTokenRevoked)

	// 5. Delete
	err = store.DeleteToken(ctx, taskID, namespace)
	require.NoError(t, err)

	_, err = store.GetToken(ctx, taskID, namespace)
	assert.ErrorIs(t, err, ErrTokenNotFound)
	_, err = indexStore.GetToken(ctx, taskID, namespace)
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

func TestKubernetesSecretTokenStore_LongTaskID(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	store := NewKubernetesSecretTokenStore(client, scheme)
	indexStore := NewKubernetesSecretTokenStore(client, scheme).WithIndexOnly()

	// Create a taskID with 250 characters (would exceed 253 with "-token" and 63 for annotation name segment)
	longTaskID := strings.Repeat("a", 250)
	namespace := "default"
	tokenData := []byte("long-id-token")

	err := store.SaveToken(ctx, longTaskID, namespace, tokenData)
	require.NoError(t, err)

	secName := secretName(longTaskID)
	assert.LessOrEqual(t, len(secName), 253)

	labelVal := TaskLabelValue(longTaskID)
	assert.Equal(t, 63, len(labelVal))

	sec := &corev1.Secret{}
	require.NoError(t, client.Get(ctx, objectKey(namespace, secName), sec))
	assert.Equal(t, labelVal, sec.Labels[LabelAgentTask])
	assert.LessOrEqual(t, len(sec.Labels[LabelAgentTask]), 63)

	annKey := revokedIndexKey(longTaskID)
	parts := strings.SplitN(annKey, "/", 2)
	require.Len(t, parts, 2)
	assert.LessOrEqual(t, len(parts[1]), 63)

	retrieved, err := store.GetToken(ctx, longTaskID, namespace)
	require.NoError(t, err)
	assert.Equal(t, tokenData, retrieved)

	err = store.RevokeToken(ctx, longTaskID, namespace)
	require.NoError(t, err)
	assert.True(t, store.IsRevoked(ctx, longTaskID, namespace))
	assert.True(t, indexStore.IsRevoked(ctx, longTaskID, namespace))

	// Verify revoked token payload is removed from index Data while revocation annotation remains
	idxSec := &corev1.Secret{}
	require.NoError(t, client.Get(ctx, objectKey(namespace, TokenIndexSecretName), idxSec))
	_, existsInData := idxSec.Data[longTaskID]
	assert.False(t, existsInData, "revoked token should be removed from index Data")
	assert.Equal(t, "true", idxSec.Annotations[annKey])

	err = store.DeleteToken(ctx, longTaskID, namespace)
	require.NoError(t, err)
}

func objectKey(namespace, name string) client.ObjectKey {
	return client.ObjectKey{Namespace: namespace, Name: name}
}

func TestKubernetesSecretTokenStore_ConcurrentUpsertAndRevoke(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	store := NewKubernetesSecretTokenStore(fakeClient, scheme)
	indexStore := NewKubernetesSecretTokenStore(fakeClient, scheme).WithIndexOnly()
	namespace := "default"

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			suffix := strings.Repeat("x", id+1)
			taskID := "task-concurrent-" + suffix
			assert.NoError(t, store.SaveToken(ctx, taskID, namespace, []byte("tok-"+suffix)))
		}(i)
	}
	wg.Wait()

	for i := 0; i < 10; i++ {
		suffix := strings.Repeat("x", i+1)
		taskID := "task-concurrent-" + suffix
		tok, err := indexStore.GetToken(ctx, taskID, namespace)
		require.NoError(t, err)
		assert.Equal(t, []byte("tok-"+suffix), tok)
	}
}

func TestKubernetesSecretTokenStore_OwnershipValidation(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	store := NewKubernetesSecretTokenStore(fakeClient, scheme)

	task1 := "task-owner-1"
	task2 := "task-owner-2"
	namespace := "default"

	// Save token for task1
	err := store.SaveToken(ctx, task1, namespace, []byte("token1"))
	require.NoError(t, err)

	// Attempting to overwrite secret with a different taskID should fail
	// Manually construct secretName collision or modify label
	name := secretName(task1)
	sec := &corev1.Secret{}
	err = fakeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sec)
	require.NoError(t, err)

	// Change label to simulate mismatch or unowned secret
	sec.Labels[LabelAgentTask] = task2
	err = fakeClient.Update(ctx, sec)
	require.NoError(t, err)

	// GetToken for task1 should now fail (ErrTokenNotFound due to mismatch)
	_, err = store.GetToken(ctx, task1, namespace)
	assert.ErrorIs(t, err, ErrTokenNotFound)

	// RevokeToken for task1 should fail
	err = store.RevokeToken(ctx, task1, namespace)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not owned by")

	// DeleteToken for task1 should fail
	err = store.DeleteToken(ctx, task1, namespace)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not owned by")
}

func TestKubernetesSecretTokenStore_IndexOnlyRevoke(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	store := NewKubernetesSecretTokenStore(fakeClient, scheme)
	indexStore := NewKubernetesSecretTokenStore(fakeClient, scheme).WithIndexOnly()

	taskID := "task-index-revoke"
	namespace := "default"

	// Revoke before index exists returns ErrTokenNotFound in indexOnly mode
	err := indexStore.RevokeToken(ctx, taskID, namespace)
	assert.ErrorIs(t, err, ErrTokenNotFound)

	// Save via controller store, then revoke via server index-only store
	require.NoError(t, store.SaveToken(ctx, taskID, namespace, []byte("tok-payload")))
	assert.False(t, indexStore.IsRevoked(ctx, taskID, namespace))

	require.NoError(t, indexStore.RevokeToken(ctx, taskID, namespace))
	assert.True(t, indexStore.IsRevoked(ctx, taskID, namespace))

	_, err = indexStore.GetToken(ctx, taskID, namespace)
	assert.ErrorIs(t, err, ErrTokenRevoked)
}

func TestKubernetesSecretTokenStore_LegacyLabelMigration(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	store := NewKubernetesSecretTokenStore(fakeClient, scheme)

	// Construct a >63 char taskID with a hyphen at position 45 so legacyTaskLabelValue
	// differs from TaskLabelValue (which trims trailing hyphens from prefix).
	taskID := strings.Repeat("a", 45) + "-" + strings.Repeat("b", 30)
	namespace := "default"

	legacyLabel := legacyTaskLabelValue(taskID)
	currentLabel := TaskLabelValue(taskID)
	require.NotEqual(t, legacyLabel, currentLabel)

	require.NoError(t, store.SaveToken(ctx, taskID, namespace, []byte("v1")))

	// Downgrade the secret's label to the legacy format
	sec := &corev1.Secret{}
	require.NoError(t, fakeClient.Get(ctx, objectKey(namespace, SecretName(taskID)), sec))
	sec.Labels[LabelAgentTask] = legacyLabel
	require.NoError(t, fakeClient.Update(ctx, sec))

	// GetToken still succeeds with legacy label
	tok, err := store.GetToken(ctx, taskID, namespace)
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), tok)

	// SaveToken succeeds and migrates the label to the current TaskLabelValue format
	require.NoError(t, store.SaveToken(ctx, taskID, namespace, []byte("v2")))
	require.NoError(t, fakeClient.Get(ctx, objectKey(namespace, SecretName(taskID)), sec))
	assert.Equal(t, currentLabel, sec.Labels[LabelAgentTask])
}
