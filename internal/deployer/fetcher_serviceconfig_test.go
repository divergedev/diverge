package deployer

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestServiceConfigFetcher_Fetch(t *testing.T) {
	fetcher := &ServiceConfigFetcher{}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mr-42",
			Namespace: "demo-bank",
		},
		Spec: v1alpha1.EnvironmentSpec{
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "payments-api",
				Port:        8080,
				Image:       "registry/payments-api:mr-42-abc",
				Env: []v1alpha1.EnvVar{
					{Name: "DB_URL", Value: "postgres://localhost/preview"},
				},
			},
		},
	}

	objs, err := fetcher.Fetch(context.Background(), env)
	require.NoError(t, err)

	require.Len(t, objs, 2, "expected 2 objects (Deployment + Service)")

	// Check Deployment
	deploy := objs[0]
	assert.Equal(t, "Deployment", deploy.GetKind())
	assert.Equal(t, "mr-42-payments-api", deploy.GetName())
	labels := deploy.GetLabels()
	assert.Equal(t, "preview", labels["diverge.io/role"])
	assert.Equal(t, "mr-42", labels["diverge.io/preview-id"])

	// Check Service
	svc := objs[1]
	assert.Equal(t, "Service", svc.GetKind())
	assert.Equal(t, "mr-42-payments-api", svc.GetName())
}

func TestServiceConfigFetcher_NilConfig(t *testing.T) {
	fetcher := &ServiceConfigFetcher{}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
	}

	_, err := fetcher.Fetch(context.Background(), env)
	require.Error(t, err, "expected error for nil serviceConfig")
}

func TestServiceConfigFetcher_EmptyImage(t *testing.T) {
	fetcher := &ServiceConfigFetcher{}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: v1alpha1.EnvironmentSpec{
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "svc",
				Port:        8080,
			},
		},
	}

	_, err := fetcher.Fetch(context.Background(), env)
	require.Error(t, err, "expected error for empty image")
}

func TestServiceConfigFetcher_EmptyServiceName(t *testing.T) {
	fetcher := &ServiceConfigFetcher{}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: v1alpha1.EnvironmentSpec{
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "",
				Port:        8080,
				Image:       "img",
			},
		},
	}

	_, err := fetcher.Fetch(context.Background(), env)
	require.ErrorContains(t, err, "serviceConfig.serviceName is required")
}

func TestServiceConfigFetcher_InvalidPorts(t *testing.T) {
	fetcher := &ServiceConfigFetcher{}

	ports := []int32{0, -1, 65536}
	for _, p := range ports {
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
			Spec: v1alpha1.EnvironmentSpec{
				ServiceConfig: &v1alpha1.ServicePreviewConfig{
					ServiceName: "svc",
					Port:        p,
					Image:       "img",
				},
			},
		}

		_, err := fetcher.Fetch(context.Background(), env)
		require.ErrorContains(t, err, "out of valid range 1-65535")
	}
}
