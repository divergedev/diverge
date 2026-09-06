package cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	coretesting "k8s.io/client-go/testing"
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

func TestResolveRemotePort_ProtocolMismatch(t *testing.T) {
	svc := corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Port:       80,
				TargetPort: intstr.FromString("dns"),
				Protocol:   corev1.ProtocolUDP,
			}},
		},
	}
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "server",
				Ports: []corev1.ContainerPort{
					{Name: "dns", ContainerPort: 53, Protocol: corev1.ProtocolTCP}, // wrong protocol
				},
			}},
		},
	}
	_, err := resolveRemotePort(svc, pod)
	require.ErrorIs(t, err, ErrNamedTargetPortNotFound, "should not match port with wrong protocol")
}

func TestResolveRemotePort_NamedPortInNativeSidecar(t *testing.T) {
	restartAlways := corev1.ContainerRestartPolicyAlways
	svc := corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromString("admin")}},
		},
	}
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "app",
				Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}},
			}},
			InitContainers: []corev1.Container{{
				Name:          "sidecar-proxy",
				RestartPolicy: &restartAlways,
				Ports:         []corev1.ContainerPort{{Name: "admin", ContainerPort: 9901}},
			}},
		},
	}
	port, err := resolveRemotePort(svc, pod)
	require.NoError(t, err)
	assert.Equal(t, 9901, port, "should find named port in native sidecar init container")
}

func TestResolveRemotePort_RegularInitContainerExcluded(t *testing.T) {
	svc := corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromString("admin")}},
		},
	}
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "app",
				Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}},
			}},
			InitContainers: []corev1.Container{{
				Name:  "init-setup", // no RestartPolicy — regular init, exits before Running
				Ports: []corev1.ContainerPort{{Name: "admin", ContainerPort: 9901}},
			}},
		},
	}
	_, err := resolveRemotePort(svc, pod)
	require.ErrorIs(t, err, ErrNamedTargetPortNotFound, "regular init containers should be excluded")
}

// chartServerLabels mirrors what charts/diverge/templates/server-service.yaml
// and server-deployment.yaml actually emit: diverge.selectorLabels renders
// app.kubernetes.io/name as the chart fullname ("diverge"), with the
// component label carrying the server distinction.
func chartServerLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":      "diverge",
		"app.kubernetes.io/instance":  "diverge",
		"app.kubernetes.io/component": "server",
		"app.kubernetes.io/version":   "v0.9.0",
		"app.kubernetes.io/part-of":   "diverge",
	}
}

// TestDiscoverServer_ChartLabels guards against discovery selecting on
// app.kubernetes.io/name=diverge-server, which no chart install ever sets.
// Reaching the pod lookup proves the Service matched; the chart-labelled
// Service alone is not enough, because the Pod lookup must use the same
// selector.
func TestDiscoverServer_ChartLabels(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-server",
			Namespace: "diverge-system",
			Labels:    chartServerLabels(),
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8443, TargetPort: intstr.FromString("grpc")}},
		},
	}
	client := fake.NewSimpleClientset(svc)

	_, _, err := discoverServer(context.Background(), client, nil)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrServerNotFound,
		"chart-labelled Service should be discovered, not reported as missing")
	assert.Contains(t, err.Error(), "pod not found or not running")
}

// TestDiscoverServer_ChartLabelsPodLookup ensures the Pod lookup reuses the
// selector that matched the Service. Labelling only the Service is what made
// the original bug survive a partial workaround, so the pod selector is
// asserted to actually select a chart-labelled pod.
func TestDiscoverServer_ChartLabelsPodLookup(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-server",
			Namespace: "diverge-system",
			Labels:    chartServerLabels(),
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8443, TargetPort: intstr.FromString("grpc")}},
		},
	}
	client := fake.NewSimpleClientset(svc)

	var podSelector string
	client.PrependReactor("list", "pods", func(action coretesting.Action) (bool, runtime.Object, error) {
		podSelector = action.(coretesting.ListAction).GetListRestrictions().Labels.String()
		return false, nil, nil
	})

	_, _, err := discoverServer(context.Background(), client, nil)
	require.Error(t, err)
	require.NotEmpty(t, podSelector, "pod lookup was never reached")

	selector, parseErr := labels.Parse(podSelector)
	require.NoError(t, parseErr)
	assert.True(t, selector.Matches(labels.Set(chartServerLabels())),
		"pod selector %q does not select chart-labelled server pods", podSelector)
}

// TestDiscoverServer_LegacyLabelsStillWork pins the backward-compatible path
// for hand-rolled manifests using the pre-chart label.
func TestDiscoverServer_LegacyLabelsStillWork(t *testing.T) {
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
	assert.NotErrorIs(t, err, ErrServerNotFound)
	assert.Contains(t, err.Error(), "pod not found or not running")
}

func TestDiscoverServer_PartOfLabelWithCustomNameOverride(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "custom-diverge-server",
			Namespace: "diverge-system",
			Labels: map[string]string{
				"app.kubernetes.io/name":      "my-custom-release",
				"app.kubernetes.io/part-of":   "diverge",
				"app.kubernetes.io/component": "server",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8443, TargetPort: intstr.FromString("grpc")}},
		},
	}
	client := fake.NewSimpleClientset(svc)

	_, _, err := discoverServer(context.Background(), client, nil)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrServerNotFound,
		"part-of=diverge Service should be discovered despite name override")
	assert.Contains(t, err.Error(), "pod not found or not running")
}

func TestDiscoverServer_RBACForbiddenClusterList_FallsBackToCandidateNamespaces(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-server",
			Namespace: "diverge-system",
			Labels:    chartServerLabels(),
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8443, TargetPort: intstr.FromString("grpc")}},
		},
	}
	client := fake.NewSimpleClientset(svc)

	// Intercept cluster-wide service list and return 403 Forbidden
	client.PrependReactor("list", "services", func(action coretesting.Action) (bool, runtime.Object, error) {
		listAction := action.(coretesting.ListAction)
		if listAction.GetNamespace() == "" {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "", fmt.Errorf("cluster-wide listing forbidden"))
		}
		// Allow namespace-specific listing to pass through
		return false, nil, nil
	})

	discoverer := &K8sServerDiscoverer{
		K8sClient: client,
		Namespace: "test-ns",
	}

	_, _, err := discoverer.Discover(context.Background())
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrServerNotFound,
		"fallback to candidate namespaces should discover service in diverge-system")
	assert.Contains(t, err.Error(), "pod not found or not running")
}

func TestDiscoverServer_SkipsTerminatingPod(t *testing.T) {
	now := metav1.Now()
	terminatingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "diverge-server-terminating",
			Namespace:         "diverge-system",
			Labels:            chartServerLabels(),
			DeletionTimestamp: &now,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	activePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-server-active",
			Namespace: "diverge-system",
			Labels:    chartServerLabels(),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	discoverer := &K8sServerDiscoverer{
		K8sClient: fake.NewSimpleClientset(terminatingPod, activePod),
	}

	pod, err := discoverer.findActivePod(context.Background(), "diverge-system", "app.kubernetes.io/component=server")
	require.NoError(t, err)
	assert.Equal(t, "diverge-server-active", pod.Name, "should select active non-terminating pod")
}

func TestDiscoverServer_AllPodsTerminating_ReturnsError(t *testing.T) {
	now := metav1.Now()
	terminatingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "diverge-server-terminating",
			Namespace:         "diverge-system",
			Labels:            chartServerLabels(),
			DeletionTimestamp: &now,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	discoverer := &K8sServerDiscoverer{
		K8sClient: fake.NewSimpleClientset(terminatingPod),
	}

	_, err := discoverer.findActivePod(context.Background(), "diverge-system", "app.kubernetes.io/component=server")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pod not found or not running")
}

type mockDiscoverer struct {
	addr    string
	stopCh  chan struct{}
	err     error
	invoked bool
}

func (m *mockDiscoverer) Discover(ctx context.Context) (string, chan struct{}, error) {
	m.invoked = true
	return m.addr, m.stopCh, m.err
}

func TestDev_WithServerDiscovererOption(t *testing.T) {
	mock := &mockDiscoverer{
		addr:   "http://mock-server:8080",
		stopCh: make(chan struct{}),
	}
	defer close(mock.stopCh)

	opt := WithServerDiscoverer(mock)
	devOpts := &DevOptions{}
	opt(devOpts)

	assert.Equal(t, mock, devOpts.Discoverer)
	addr, _, err := devOpts.Discoverer.Discover(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "http://mock-server:8080", addr)
	assert.True(t, mock.invoked)
}
