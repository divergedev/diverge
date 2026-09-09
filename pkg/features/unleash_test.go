package features

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func setupUnleashTestClient(t *testing.T, objects ...runtime.Object) *UnleashProvider {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()
	logger := logr.Discard()

	return NewUnleashProvider(client, scheme, logger)
}

func TestUnleashProvision_WithOverrides(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash-creds",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url":   []byte(server.URL),
			"token": []byte("test-token"),
		},
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				ConnectionRef: "unleash-creds",
				Overrides: map[string]string{
					"feature-a": "true",
					"feature-b": "false",
				},
			},
		},
	}

	provider := setupUnleashTestClient(t, secret)

	result, err := provider.Provision(context.Background(), env)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "unleash", result.ProviderType)
	assert.Equal(t, server.URL, result.EnvVars["UNLEASH_URL"])
	assert.Equal(t, "diverge-my-env", result.EnvVars["UNLEASH_APP_NAME"])
	assert.Equal(t, "my-env", result.EnvVars["UNLEASH_ENVIRONMENT"])
	assert.Equal(t, "test-token", result.EnvVars["UNLEASH_API_TOKEN"])
}

func TestUnleashProvision_SecretResolution(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash-connection",
			Namespace: "diverge-system",
		},
		Data: map[string][]byte{
			"url":   []byte("http://unleash.diverge-system.svc.cluster.local:4242"),
			"token": []byte("system-token"),
		},
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{},
		},
	}

	provider := setupUnleashTestClient(t, secret)

	result, err := provider.Provision(context.Background(), env)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "system-token", result.EnvVars["UNLEASH_API_TOKEN"])
}

func TestUnleashProvision_SSRFRejection(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash-creds",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url": []byte("file:///etc/passwd"),
		},
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				ConnectionRef: "unleash-creds",
			},
		},
	}

	provider := setupUnleashTestClient(t, secret)

	_, err := provider.Provision(context.Background(), env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid unleash url scheme")
}

func TestUnleashTeardown_GracefulOnMissingSecret(t *testing.T) {
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				ConnectionRef: "missing-creds",
			},
		},
	}

	provider := setupUnleashTestClient(t)

	err := provider.Teardown(context.Background(), env)
	assert.NoError(t, err) // Should not block finalizer
}

func TestUnleashTeardown_Success(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash-creds",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url": []byte("http://unleash.test"),
		},
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				ConnectionRef: "unleash-creds",
			},
		},
	}

	provider := setupUnleashTestClient(t, secret)

	err := provider.Teardown(context.Background(), env)
	assert.NoError(t, err)
}

func TestUnleashStatus_Healthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash-creds",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url": []byte(server.URL),
		},
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				ConnectionRef: "unleash-creds",
				Overrides: map[string]string{
					"f1": "true",
				},
			},
		},
	}

	provider := setupUnleashTestClient(t, secret)

	status, err := provider.Status(context.Background(), env)
	require.NoError(t, err)
	assert.True(t, status.Ready)
}

func TestUnleashStatus_Unhealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash-creds",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url": []byte(server.URL),
		},
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				ConnectionRef: "unleash-creds",
				Overrides: map[string]string{
					"f1": "true",
				},
			},
		},
	}

	provider := setupUnleashTestClient(t, secret)

	status, err := provider.Status(context.Background(), env)
	require.NoError(t, err)
	assert.False(t, status.Ready)
}
