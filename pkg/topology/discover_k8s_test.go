package topology

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDiscoverPrometheusURL_LabelPriority(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "prom-fallback",
				Namespace: "monitoring",
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{Port: 9090}},
			},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "prom-primary",
				Namespace: "custom-ns",
				Labels: map[string]string{
					"app.kubernetes.io/name": "prometheus",
				},
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{Port: 9091}},
			},
		},
	)

	url, err := DiscoverPrometheusURL(context.Background(), client)
	require.NoError(t, err)
	assert.Equal(t, "http://prom-primary.custom-ns.svc:9091", url)
}

func TestDiscoverPrometheusURL_Fallback(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "prometheus-server",
				Namespace: "monitoring",
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{Port: 9090}},
			},
		},
	)

	url, err := DiscoverPrometheusURL(context.Background(), client)
	require.NoError(t, err)
	assert.Equal(t, "http://prometheus-server.monitoring.svc:9090", url)
}

func TestDiscoverPrometheusURL_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-service",
				Namespace: "monitoring",
			},
		},
	)

	_, err := DiscoverPrometheusURL(context.Background(), client)
	require.Error(t, err)
}
