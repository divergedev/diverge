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
	assert.Equal(t, "preview", labels["divergedev.com/role"])
	assert.Equal(t, "mr-42", labels["divergedev.com/preview-id"])

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

func TestServiceConfigFetcher_FeatureFlagInjection(t *testing.T) {
	fetcher := &ServiceConfigFetcher{}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-42",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider: "configmap",
				Overrides: map[string]string{
					"checkout_v2": "true",
				},
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "api",
				Port:        8080,
				Image:       "api:latest",
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			FeatureConfigMap: "diverge-features-pr-42",
			FeatureEnvVars: map[string]string{
				"CUSTOM_FLAG_VAR": "val-123",
			},
		},
	}

	objs, err := fetcher.Fetch(context.Background(), env)
	require.NoError(t, err)
	require.Len(t, objs, 2)

	deploy := objs[0]
	spec := deploy.Object["spec"].(map[string]interface{})
	tmpl := spec["template"].(map[string]interface{})
	podSpec := tmpl["spec"].(map[string]interface{})

	// Check volume
	volumes, ok := podSpec["volumes"].([]interface{})
	require.True(t, ok)
	require.Len(t, volumes, 1)
	vol := volumes[0].(map[string]interface{})
	assert.Equal(t, "diverge-features", vol["name"])
	cmRef := vol["configMap"].(map[string]interface{})
	assert.Equal(t, "diverge-features-pr-42", cmRef["name"])

	// Check container
	containers := podSpec["containers"].([]interface{})
	require.Len(t, containers, 1)
	container := containers[0].(map[string]interface{})

	// Check volumeMount
	mounts, ok := container["volumeMounts"].([]interface{})
	require.True(t, ok)
	require.Len(t, mounts, 1)
	mount := mounts[0].(map[string]interface{})
	assert.Equal(t, "diverge-features", mount["name"])
	assert.Equal(t, "/etc/diverge/flags", mount["mountPath"])
	assert.Equal(t, true, mount["readOnly"])

	// Check env vars
	envs := container["env"].([]interface{})
	envMap := make(map[string]string)
	for _, raw := range envs {
		entry := raw.(map[string]interface{})
		if val, ok := entry["value"].(string); ok {
			envMap[entry["name"].(string)] = val
		}
	}
	assert.Equal(t, "diverge-features-pr-42", envMap["DIVERGE_FEATURE_CONFIGMAP"])
	assert.Equal(t, "/etc/diverge/flags/flags.json", envMap["FLAGD_FLAG_PATH"])
	assert.Equal(t, "val-123", envMap["CUSTOM_FLAG_VAR"])
}
