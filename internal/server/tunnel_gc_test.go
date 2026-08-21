package server

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestTunnelGC_SweepsExpiredService(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	gc := NewTunnelGC(fakeK8s, logger)

	// Create an expired tunnel service
	expired := time.Now().Add(-1 * time.Minute).Format(time.RFC3339)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-tunnel-old",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.dev/tunnel": "true",
			},
			Annotations: map[string]string{
				"diverge.dev/tunnel-expires": expired,
			},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
		},
	}

	_, err := fakeK8s.CoreV1().Services("default").Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	// Verify it exists
	_, err = fakeK8s.CoreV1().Services("default").Get(context.Background(), "diverge-tunnel-old", metav1.GetOptions{})
	require.NoError(t, err)

	// Run sweep
	gc.sweep(context.Background(), "default")

	// Should be deleted
	_, err = fakeK8s.CoreV1().Services("default").Get(context.Background(), "diverge-tunnel-old", metav1.GetOptions{})
	assert.Error(t, err, "expired service should be deleted")
}

func TestTunnelGC_KeepsNonExpiredService(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	gc := NewTunnelGC(fakeK8s, logger)

	// Create a non-expired tunnel service
	future := time.Now().Add(5 * time.Minute).Format(time.RFC3339)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-tunnel-active",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.dev/tunnel": "true",
			},
			Annotations: map[string]string{
				"diverge.dev/tunnel-expires": future,
			},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
		},
	}

	_, err := fakeK8s.CoreV1().Services("default").Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	gc.sweep(context.Background(), "default")

	// Should still exist
	_, err = fakeK8s.CoreV1().Services("default").Get(context.Background(), "diverge-tunnel-active", metav1.GetOptions{})
	assert.NoError(t, err, "non-expired service should be kept")
}

func TestTunnelGC_IgnoresServicesWithoutAnnotation(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	gc := NewTunnelGC(fakeK8s, logger)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-tunnel-noannot",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.dev/tunnel": "true",
			},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
		},
	}

	_, err := fakeK8s.CoreV1().Services("default").Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	gc.sweep(context.Background(), "default")

	_, err = fakeK8s.CoreV1().Services("default").Get(context.Background(), "diverge-tunnel-noannot", metav1.GetOptions{})
	assert.NoError(t, err, "service without annotation should be kept")
}
