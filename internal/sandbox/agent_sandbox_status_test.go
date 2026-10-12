//go:build !no_sandbox

package sandbox

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (t *trackingReadCloser) Close() error {
	t.closed = true
	return nil
}

func TestAgentSandboxProvider_StatusConditionsAndPodDetails(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-status-1",
			Namespace: "default",
		},
	}

	// 1. SandboxClaim not found -> Pending, Ready=false
	emptyClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	provider := NewAgentSandboxProvider(emptyClient, nil, scheme)

	st, err := provider.Status(ctx, task)
	require.NoError(t, err)
	assert.Equal(t, "sb-claim-task-status-1", st.ClaimName)
	assert.Equal(t, "Pending", st.Phase)
	assert.False(t, st.Ready)
	assert.Equal(t, "SandboxClaim not found", st.Message)

	// 2. SandboxClaim with Ready=False condition and Pod details
	claimNotReady := &unstructured.Unstructured{}
	claimNotReady.SetGroupVersionKind(sandboxClaimGVK)
	claimNotReady.SetName("sb-claim-task-status-1")
	claimNotReady.SetNamespace("default")
	claimNotReady.Object["status"] = map[string]interface{}{
		"podName": "sandbox-pod-1",
		"podIP":   "10.244.0.15",
		"phase":   "Bound",
		"conditions": []interface{}{
			map[string]interface{}{
				"type":   "Ready",
				"status": "False",
			},
		},
	}

	clientNotReady := fake.NewClientBuilder().WithScheme(scheme).WithObjects(claimNotReady).Build()
	providerNotReady := NewAgentSandboxProvider(clientNotReady, nil, scheme)

	st, err = providerNotReady.Status(ctx, task)
	require.NoError(t, err)
	assert.Equal(t, "sb-claim-task-status-1", st.ClaimName)
	assert.Equal(t, "sandbox-pod-1", st.PodName)
	assert.Equal(t, "10.244.0.15", st.PodIP)
	assert.Equal(t, "Bound", st.Phase)
	assert.False(t, st.Ready)
	assert.Equal(t, "Sandbox phase: Bound", st.Message)

	// 3. SandboxClaim with Ready=True condition and empty phase (defaults to Pending)
	claimReady := &unstructured.Unstructured{}
	claimReady.SetGroupVersionKind(sandboxClaimGVK)
	claimReady.SetName("sb-claim-task-status-1")
	claimReady.SetNamespace("default")
	claimReady.Object["status"] = map[string]interface{}{
		"podName": "sandbox-pod-ready",
		"podIP":   "10.244.0.99",
		"phase":   "Running",
		"conditions": []interface{}{
			map[string]interface{}{
				"type":   "Initialized",
				"status": "True",
			},
			map[string]interface{}{
				"type":   "Ready",
				"status": "True",
			},
		},
	}

	clientReady := fake.NewClientBuilder().WithScheme(scheme).WithObjects(claimReady).Build()
	providerReady := NewAgentSandboxProvider(clientReady, nil, scheme)

	st, err = providerReady.Status(ctx, task)
	require.NoError(t, err)
	assert.Equal(t, "sandbox-pod-ready", st.PodName)
	assert.Equal(t, "10.244.0.99", st.PodIP)
	assert.Equal(t, "Running", st.Phase)
	assert.True(t, st.Ready)
	assert.Equal(t, "Sandbox phase: Running", st.Message)

	// 4. StreamLogs fallback when clientset is nil and pod is scheduled
	rc, err := providerReady.StreamLogs(ctx, task, pkgsandbox.LogOptions{Follow: true})
	require.NoError(t, err)
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Contains(t, string(data), "clientset not available")
	require.NoError(t, rc.Close())

	// 5. StreamLogs error when podName is empty
	_, err = provider.StreamLogs(ctx, task, pkgsandbox.LogOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox pod not yet scheduled")
}

func TestCancelableReadCloser_Close(t *testing.T) {
	canceled := false
	inner := &trackingReadCloser{Reader: bytes.NewBufferString("log line\n")}
	crc := &cancelableReadCloser{
		ReadCloser: inner,
		cancel: func() {
			canceled = true
		},
	}

	buf, err := io.ReadAll(crc)
	require.NoError(t, err)
	assert.Equal(t, "log line\n", string(buf))

	require.NoError(t, crc.Close())
	assert.True(t, canceled, "cancel function must be called on Close()")
	assert.True(t, inner.closed, "underlying ReadCloser must be closed on Close()")
}
