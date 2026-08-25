package deployer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestOTelAnnotationDeployer_DirectDeployer_Integration(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()

	fetcher := &mockFetcher{
		objects: []unstructured.Unstructured{
			testDeploymentUnstructured("app1", ""),
		},
	}

	directDeployer := &DirectDeployer{
		Client:  c,
		Fetcher: fetcher,
	}

	otelDeployer := &OTelAnnotationDeployer{
		Inner: directDeployer,
		Annotations: map[string]string{
			"instrumentation.opentelemetry.io/inject-java": "true",
		},
	}

	env := testEnv("test-env", "test-ns", "same")
	err := otelDeployer.Deploy(context.Background(), env)
	require.NoError(t, err)

	// Verify the environment has the annotation
	assert.Equal(t, "true", env.Annotations["instrumentation.opentelemetry.io/inject-java"])

	// Verify the underlying resource got the annotation in its pod template
	var dep appsv1.Deployment
	err = c.Get(context.Background(), client.ObjectKey{Name: "app1", Namespace: "test-ns"}, &dep)
	require.NoError(t, err)

	podAnnotations := dep.Spec.Template.Annotations
	require.NotNil(t, podAnnotations)
	assert.Equal(t, "true", podAnnotations["instrumentation.opentelemetry.io/inject-java"])
}
