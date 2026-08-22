package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestTunnelDiscovery_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	_, _, err := discoverServer(context.Background(), client, nil)
	assert.ErrorIs(t, err, ErrServerNotFound)
}

func TestTunnelDiscovery_Found(t *testing.T) {
	t.Skip("TODO: needs integration test support for SPDY port-forward")
	client := fake.NewSimpleClientset(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-server",
			Namespace: "diverge-system",
			Labels: map[string]string{
				"app.kubernetes.io/name": "diverge-server",
			},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	url, stopCh, err := discoverServer(ctx, client, nil)
	require.NoError(t, err)
	if stopCh != nil {
		close(stopCh)
	}
	assert.Equal(t, "http://localhost:18080", url)
}
