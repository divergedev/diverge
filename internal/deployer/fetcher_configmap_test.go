package deployer

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestConfigMapFetcher_Fetch_DeterministicOrder(t *testing.T) {
	s := testScheme()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "manifests-cm",
			Namespace: "default",
			Labels: map[string]string{
				"divergedev.com/manifests":   "true",
				"divergedev.com/environment": "pr-10",
			},
		},
		Data: map[string]string{
			"02-service.yaml":    "apiVersion: v1\nkind: Service\nmetadata:\n  name: svc-b\n",
			"01-deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: dep-a\n",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(cm).Build()
	fetcher := &ConfigMapFetcher{Client: c}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-10", Namespace: "default"},
	}

	objs, err := fetcher.Fetch(context.Background(), env)
	require.NoError(t, err)
	require.Len(t, objs, 2)
	assert.Equal(t, "Deployment", objs[0].GetKind())
	assert.Equal(t, "dep-a", objs[0].GetName())
	assert.Equal(t, "Service", objs[1].GetKind())
	assert.Equal(t, "svc-b", objs[1].GetName())
}

func TestConfigMapFetcher_Fetch_InvalidYAML(t *testing.T) {
	s := testScheme()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bad-cm",
			Namespace: "default",
			Labels: map[string]string{
				"divergedev.com/manifests":   "true",
				"divergedev.com/environment": "pr-10",
			},
		},
		Data: map[string]string{
			"broken.yaml": ":::not valid yaml\t\t{{{",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(cm).Build()
	fetcher := &ConfigMapFetcher{Client: c}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-10", Namespace: "default"},
	}

	_, err := fetcher.Fetch(context.Background(), env)
	require.Error(t, err)
}
