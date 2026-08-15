package knative

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildKnativeService_Basic(t *testing.T) {
	labels := map[string]string{"foo": "bar"}
	annotations := map[string]string{"anno": "test"}

	svc, err := BuildKnativeService("test-svc", "default", "nginx:latest", 8080, labels, annotations)
	require.NoError(t, err)
	require.NotNil(t, svc)

	assert.Equal(t, "test-svc", svc.Name)
	assert.Equal(t, "default", svc.Namespace)

	assert.Equal(t, "bar", svc.Labels["foo"])
	assert.Equal(t, "diverge", svc.Labels["diverge.io/managed-by"])

	assert.Equal(t, "test", svc.Annotations["anno"])
	assert.Equal(t, "IgnoreExtraneous", svc.Annotations["argocd.argoproj.io/compare-options"])
	assert.Equal(t, "0", svc.Annotations["autoscaling.knative.dev/min-scale"])

	require.Len(t, svc.Spec.ConfigurationSpec.Template.Spec.PodSpec.Containers, 1)
	assert.Equal(t, "nginx:latest", svc.Spec.ConfigurationSpec.Template.Spec.PodSpec.Containers[0].Image)
	assert.Equal(t, int32(8080), svc.Spec.ConfigurationSpec.Template.Spec.PodSpec.Containers[0].Ports[0].ContainerPort)
}

func TestBuildKnativeService_NilLabels(t *testing.T) {
	svc, err := BuildKnativeService("test-svc", "default", "nginx:latest", 8080, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, "diverge", svc.Labels["diverge.io/managed-by"])
}

func TestBuildKnativeService_NilAnnotations(t *testing.T) {
	svc, err := BuildKnativeService("test-svc", "default", "nginx:latest", 8080, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, "IgnoreExtraneous", svc.Annotations["argocd.argoproj.io/compare-options"])
}

func TestBuildKnativeService_ScaleToZero(t *testing.T) {
	svc, err := BuildKnativeService("test-svc", "default", "nginx:latest", 8080, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "0", svc.Annotations["autoscaling.knative.dev/min-scale"])
}

func TestBuildKnativeService_InvalidLabelKey(t *testing.T) {
	_, err := BuildKnativeService("test-svc", "default", "img", 80, map[string]string{"inv alid": "v"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid label key")
}

func TestBuildKnativeService_InvalidLabelValue(t *testing.T) {
	longVal := "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" // 64 chars
	_, err := BuildKnativeService("test-svc", "default", "img", 80, map[string]string{"k": longVal}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid label value")
}

func TestBuildKnativeService_ValidLabelsPassthrough(t *testing.T) {
	labels := map[string]string{"custom": "valid"}
	svc, err := BuildKnativeService("test-svc", "default", "img", 80, labels, nil)
	require.NoError(t, err)

	assert.Equal(t, "valid", svc.Labels["custom"])
	assert.Equal(t, "valid", svc.Spec.ConfigurationSpec.Template.Labels["custom"])
}
