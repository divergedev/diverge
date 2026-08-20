package server

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"
)

func TestTunnelLease_AcquireNew(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	tl := NewTunnelLease(fakeK8s, logger, "pod-1")

	prev, err := tl.Acquire(context.Background(), "default", "my-preview", "tunnel-abc")
	require.NoError(t, err)
	assert.Empty(t, prev, "no previous holder on first acquire")
}

func TestTunnelLease_AcquireSteal(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	tl := NewTunnelLease(fakeK8s, logger, "pod-1")

	// First acquire
	_, err := tl.Acquire(context.Background(), "default", "my-preview", "tunnel-1")
	require.NoError(t, err)

	// Second acquire steals
	prev, err := tl.Acquire(context.Background(), "default", "my-preview", "tunnel-2")
	require.NoError(t, err)
	assert.Equal(t, "pod-1:tunnel-1", prev)
}

func TestTunnelLease_RenewSuccess(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	tl := NewTunnelLease(fakeK8s, logger, "pod-1")

	_, err := tl.Acquire(context.Background(), "default", "my-preview", "tunnel-abc")
	require.NoError(t, err)

	ok := tl.Renew(context.Background(), "default", "my-preview", "tunnel-abc")
	assert.True(t, ok, "should renew successfully when we hold the lease")
}

func TestTunnelLease_RenewFailsAfterSteal(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	tl := NewTunnelLease(fakeK8s, logger, "pod-1")

	_, err := tl.Acquire(context.Background(), "default", "my-preview", "tunnel-1")
	require.NoError(t, err)

	// Another tunnel steals
	_, err = tl.Acquire(context.Background(), "default", "my-preview", "tunnel-2")
	require.NoError(t, err)

	// Old tunnel tries to renew — should fail
	ok := tl.Renew(context.Background(), "default", "my-preview", "tunnel-1")
	assert.False(t, ok, "should fail to renew after lease is stolen")
}

func TestTunnelLease_Release(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	tl := NewTunnelLease(fakeK8s, logger, "pod-1")

	_, err := tl.Acquire(context.Background(), "default", "my-preview", "tunnel-abc")
	require.NoError(t, err)

	tl.Release(context.Background(), "default", "my-preview", "tunnel-abc")

	// After release, renew should fail
	ok := tl.Renew(context.Background(), "default", "my-preview", "tunnel-abc")
	assert.False(t, ok, "should fail to renew after release")
}

func TestTunnelLease_ReleaseOnlyIfHolder(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	tl := NewTunnelLease(fakeK8s, logger, "pod-1")

	_, err := tl.Acquire(context.Background(), "default", "my-preview", "tunnel-1")
	require.NoError(t, err)

	// Steal with tunnel-2
	_, err = tl.Acquire(context.Background(), "default", "my-preview", "tunnel-2")
	require.NoError(t, err)

	// Old holder tries to release — should NOT delete since it doesn't hold anymore
	tl.Release(context.Background(), "default", "my-preview", "tunnel-1")

	// tunnel-2 should still be able to renew
	ok := tl.Renew(context.Background(), "default", "my-preview", "tunnel-2")
	assert.True(t, ok, "new holder should still be active after old holder's release attempt")
}
