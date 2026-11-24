package streaming

import (
	"context"
	"log/slog"
	"testing"
	"time"

	divergev1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestInformerManager_StartPolling(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, divergev1alpha1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewInformerManager(slog.Default(), BroadcasterMetrics{})
	defer mgr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	envSub, err := mgr.EnvBroadcaster.Subscribe(ctx)
	require.NoError(t, err)

	pgSub, err := mgr.PgBroadcaster.Subscribe(ctx)
	require.NoError(t, err)

	mgr.StartPolling(ctx, fakeClient, 15*time.Millisecond)

	// 1. Create Environment and PreviewGroup -> expect ADDED events
	env := &divergev1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-1",
			Namespace: "default",
		},
	}
	require.NoError(t, fakeClient.Create(ctx, env))

	pg := &divergev1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pg-1",
			Namespace: "default",
		},
	}
	require.NoError(t, fakeClient.Create(ctx, pg))

	select {
	case ev := <-envSub.Events():
		assert.Equal(t, "ADDED", ev.Type)
		assert.Equal(t, "env-1", ev.Object.Name)
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for ADDED Environment event")
	}

	select {
	case ev := <-pgSub.Events():
		assert.Equal(t, "ADDED", ev.Type)
		assert.Equal(t, "pg-1", ev.Object.Name)
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for ADDED PreviewGroup event")
	}

	// 2. Update Environment and PreviewGroup -> expect MODIFIED events
	env.Labels = map[string]string{"updated": "true"}
	require.NoError(t, fakeClient.Update(ctx, env))

	pg.Labels = map[string]string{"updated": "true"}
	require.NoError(t, fakeClient.Update(ctx, pg))

	select {
	case ev := <-envSub.Events():
		assert.Equal(t, "MODIFIED", ev.Type)
		assert.Equal(t, "true", ev.Object.Labels["updated"])
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for MODIFIED Environment event")
	}

	select {
	case ev := <-pgSub.Events():
		assert.Equal(t, "MODIFIED", ev.Type)
		assert.Equal(t, "true", ev.Object.Labels["updated"])
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for MODIFIED PreviewGroup event")
	}

	// 3. Delete Environment and PreviewGroup -> expect DELETED events
	require.NoError(t, fakeClient.Delete(ctx, env))
	require.NoError(t, fakeClient.Delete(ctx, pg))

	select {
	case ev := <-envSub.Events():
		assert.Equal(t, "DELETED", ev.Type)
		assert.Equal(t, "env-1", ev.Object.Name)
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for DELETED Environment event")
	}

	select {
	case ev := <-pgSub.Events():
		assert.Equal(t, "DELETED", ev.Type)
		assert.Equal(t, "pg-1", ev.Object.Name)
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for DELETED PreviewGroup event")
	}
}

func TestInformerManager_SynchronousInitialPollAndSnapshot(t *testing.T) {
	var nilMgr *InformerManager
	assert.Nil(t, nilMgr.SnapshotEnvironments())

	scheme := runtime.NewScheme()
	require.NoError(t, divergev1alpha1.AddToScheme(scheme))

	initialEnv := &divergev1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-init",
			Namespace: "ns-a",
			Labels:    map[string]string{"env": "initial"},
		},
		Status: divergev1alpha1.EnvironmentStatus{
			Phase: divergev1alpha1.PhaseRunning,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initialEnv).Build()

	mgr := NewInformerManager(slog.Default(), BroadcasterMetrics{})
	defer mgr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use 1h interval so only the synchronous initial poll populates SnapshotEnvironments.
	mgr.StartPolling(ctx, fakeClient, time.Hour)

	snap := mgr.SnapshotEnvironments()
	require.Len(t, snap, 1)
	assert.Equal(t, "env-init", snap[0].Name)
	assert.Equal(t, "ns-a", snap[0].Namespace)

	// Mutating returned slice items must not affect internal cached snapshots (DeepCopy isolation).
	snap[0].Name = "mutated"
	snap[0].Labels["env"] = "mutated"

	snap2 := mgr.SnapshotEnvironments()
	require.Len(t, snap2, 1)
	assert.Equal(t, "env-init", snap2[0].Name)
	assert.Equal(t, "initial", snap2[0].Labels["env"])

	// Subsequent polling updates and deletes reflected in SnapshotEnvironments.
	mgrLive := NewInformerManager(slog.Default(), BroadcasterMetrics{})
	defer mgrLive.Close()
	liveCtx, liveCancel := context.WithCancel(context.Background())
	defer liveCancel()

	mgrLive.StartPolling(liveCtx, fakeClient, 15*time.Millisecond)
	require.Len(t, mgrLive.SnapshotEnvironments(), 1)

	require.NoError(t, fakeClient.Delete(liveCtx, initialEnv))
	require.Eventually(t, func() bool {
		return len(mgrLive.SnapshotEnvironments()) == 0
	}, time.Second, 15*time.Millisecond)
}

type deadlineCheckingClient struct {
	client.Client
	listDeadlines []bool
}

func (c *deadlineCheckingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	_, ok := ctx.Deadline()
	c.listDeadlines = append(c.listDeadlines, ok)
	return c.Client.List(ctx, list, opts...)
}

func TestInformerManager_InitialPollHasDeadline(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, divergev1alpha1.AddToScheme(scheme))

	wrapped := &deadlineCheckingClient{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
	}
	mgr := NewInformerManager(slog.Default(), BroadcasterMetrics{})
	defer mgr.Close()

	mgr.StartPolling(context.Background(), wrapped, time.Hour)
	require.Len(t, wrapped.listDeadlines, 2)
	assert.True(t, wrapped.listDeadlines[0], "initial Environment List must use bounded deadline")
	assert.True(t, wrapped.listDeadlines[1], "initial PreviewGroup List must use bounded deadline")
}
