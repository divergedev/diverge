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
