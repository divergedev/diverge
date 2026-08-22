package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func TestTunnelDiscovery_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	_, _, err := discoverServer(context.Background(), client, nil)
	assert.ErrorIs(t, err, ErrServerNotFound)
}

func TestDiscoverServer_ServiceFoundNoPod(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-server",
			Namespace: "diverge-system",
			Labels:    map[string]string{"app.kubernetes.io/name": "diverge-server"},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8080, TargetPort: intstr.FromInt32(8080)}},
		},
	}
	client := fake.NewSimpleClientset(svc)
	_, _, err := discoverServer(context.Background(), client, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pod not found or not running")
}

// --- resolveRemotePort unit tests ---

func TestResolveRemotePort_NumericTargetPort(t *testing.T) {
	svc := corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt32(9090)}},
		},
	}
	pod := corev1.Pod{}
	port, err := resolveRemotePort(svc, pod)
	require.NoError(t, err)
	assert.Equal(t, 9090, port)
}

func TestResolveRemotePort_NamedTargetPort(t *testing.T) {
	svc := corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromString("grpc")}},
		},
	}
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "server",
				Ports: []corev1.ContainerPort{
					{Name: "http", ContainerPort: 8080},
					{Name: "grpc", ContainerPort: 50051},
				},
			}},
		},
	}
	port, err := resolveRemotePort(svc, pod)
	require.NoError(t, err)
	assert.Equal(t, 50051, port)
}

func TestResolveRemotePort_NamedTargetPortNotFound(t *testing.T) {
	svc := corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromString("missing-port")}},
		},
	}
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "server",
				Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}},
			}},
		},
	}
	_, err := resolveRemotePort(svc, pod)
	require.ErrorIs(t, err, ErrNamedTargetPortNotFound)
	assert.Contains(t, err.Error(), `"missing-port"`)
}

func TestResolveRemotePort_FallbackToServicePort(t *testing.T) {
	svc := corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 3000}},
		},
	}
	pod := corev1.Pod{}
	port, err := resolveRemotePort(svc, pod)
	require.NoError(t, err)
	assert.Equal(t, 3000, port)
}

func TestResolveRemotePort_Default(t *testing.T) {
	svc := corev1.Service{}
	pod := corev1.Pod{}
	port, err := resolveRemotePort(svc, pod)
	require.NoError(t, err)
	assert.Equal(t, 8080, port, "should default to 8080 when no ports specified")
}

func TestResolveRemotePort_NamedPortMultipleContainers(t *testing.T) {
	svc := corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromString("metrics")}},
		},
	}
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}},
				},
				{
					Name:  "sidecar",
					Ports: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 9100}},
				},
			},
		},
	}
	port, err := resolveRemotePort(svc, pod)
	require.NoError(t, err)
	assert.Equal(t, 9100, port, "should find named port across containers")
}
