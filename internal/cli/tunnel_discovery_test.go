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
	_, err := discoverServer(context.Background(), client)
	assert.ErrorIs(t, err, ErrServerNotFound)
}

func TestTunnelDiscovery_Found(t *testing.T) {
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

	url, err := discoverServer(ctx, client)
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:18080", url)
}
