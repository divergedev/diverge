package deployer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestConfigMapFetcher_ParsesMultiDocYAML(t *testing.T) {
	yamlData := `
apiVersion: v1
kind: Service
metadata:
  name: my-service
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-deployment
`

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "manifests-cm",
			Namespace: "test-ns",
			Labels: map[string]string{
				"diverge.io/manifests":   "true",
				"diverge.io/environment": "test-env",
			},
		},
		Data: map[string]string{
			"manifests.yaml": yamlData,
		},
	}

	c := fake.NewClientBuilder().WithObjects(cm).Build()
	fetcher := &ConfigMapFetcher{Client: c}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Namespace: "same",
			},
		},
	}

	objs, err := fetcher.Fetch(context.Background(), env)
	require.NoError(t, err)
	require.Len(t, objs, 2)
	assert.Equal(t, "Service", objs[0].GetKind())
	assert.Equal(t, "Deployment", objs[1].GetKind())
}

func TestConfigMapFetcher_NoManifests(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	fetcher := &ConfigMapFetcher{Client: c}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Namespace: "same",
			},
		},
	}

	objs, err := fetcher.Fetch(context.Background(), env)
	require.NoError(t, err)
	assert.Len(t, objs, 0)
}

func TestConfigMapFetcher_WrongLabels(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "manifests-cm",
			Namespace: "test-ns",
			Labels: map[string]string{
				"diverge.io/environment": "test-env", // missing diverge.io/manifests
			},
		},
		Data: map[string]string{
			"manifests.yaml": "kind: Pod\napiVersion: v1\nmetadata:\n  name: p",
		},
	}

	c := fake.NewClientBuilder().WithObjects(cm).Build()
	fetcher := &ConfigMapFetcher{Client: c}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Namespace: "same",
			},
		},
	}

	objs, err := fetcher.Fetch(context.Background(), env)
	require.NoError(t, err)
	assert.Len(t, objs, 0)
}

func TestURLFetcher_FetchesAndParses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("kind: Pod\napiVersion: v1\nmetadata:\n  name: my-pod"))
	}))
	defer ts.Close()

	fetcher := &URLFetcher{
		HTTPClient: ts.Client(),
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "test-ns"},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Manifests: &v1alpha1.ManifestSource{
					URL: ts.URL,
				},
			},
		},
	}

	objs, err := fetcher.Fetch(context.Background(), env)
	require.NoError(t, err)
	require.Len(t, objs, 1)
	assert.Equal(t, "Pod", objs[0].GetKind())
}

func TestURLFetcher_AuthHeader(t *testing.T) {
	var authHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("kind: Pod\napiVersion: v1\nmetadata:\n  name: my-pod"))
	}))
	defer ts.Close()

	fetcher := &URLFetcher{
		HTTPClient: ts.Client(),
		AuthToken:  "secret-token",
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "test-ns"},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Manifests: &v1alpha1.ManifestSource{
					URL: ts.URL,
				},
			},
		},
	}

	_, err := fetcher.Fetch(context.Background(), env)
	require.NoError(t, err)
	assert.Equal(t, "Bearer secret-token", authHeader)
}

func TestURLFetcher_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	fetcher := &URLFetcher{
		HTTPClient: ts.Client(),
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "test-ns"},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Manifests: &v1alpha1.ManifestSource{
					URL: ts.URL,
				},
			},
		},
	}

	_, err := fetcher.Fetch(context.Background(), env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 500")
}

func TestURLFetcher_MissingURL(t *testing.T) {
	fetcher := &URLFetcher{
		HTTPClient: http.DefaultClient,
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "test-ns"},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Manifests: nil,
			},
		},
	}

	_, err := fetcher.Fetch(context.Background(), env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manifest URL not specified")
}
