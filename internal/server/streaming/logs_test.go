package streaming

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLogStreamer_StreamPodLogs(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	streamer := NewLogStreamer(fakeK8s)
	require.NotNil(t, streamer)

	since := time.Now().Add(-5 * time.Minute)
	rc, err := streamer.StreamPodLogs(context.Background(), "default", "my-pod", "app", true, 50, &since)
	require.NoError(t, err)
	require.NotNil(t, rc)
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Contains(t, string(data), "fake logs")
}

func TestLogStreamer_StreamPodLogs_DefaultOptions(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	streamer := NewLogStreamer(fakeK8s)

	rc, err := streamer.StreamPodLogs(context.Background(), "default", "my-pod", "", false, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, rc)
	_ = rc.Close()
}
