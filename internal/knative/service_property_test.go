package knative

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/validation"
	"pgregory.net/rapid"
)

func TestBuildKnativeService_Property_ValidLabelsAccepted(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate valid label keys and values
		validKeyGen := rapid.StringMatching(`^[a-z0-9A-Z]([a-z0-9A-Z_-]{0,61}[a-z0-9A-Z])?$`).Filter(func(v string) bool {
			return len(validation.IsQualifiedName(v)) == 0
		})
		validValGen := rapid.StringMatching(`^([a-z0-9A-Z]([a-z0-9A-Z_-]{0,61}[a-z0-9A-Z])?)?$`).Filter(func(v string) bool {
			return len(validation.IsValidLabelValue(v)) == 0
		})

		labels := rapid.MapOfN(validKeyGen, validValGen, 0, 10).Draw(rt, "labels")

		svc, err := BuildKnativeService("test", "default", "img", 80, labels, nil)
		require.NoError(t, err)
		require.NotNil(t, svc)
	})
}

func TestBuildKnativeService_Property_InvalidLabelsRejected(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		invalidStrGen := rapid.StringMatching(`[!@#\$%\^&\*\(\) \n\t]+`)

		// Either the key is invalid, or the value is invalid
		invalidKey := rapid.Bool().Draw(rt, "invalidKey")

		var labels map[string]string
		if invalidKey {
			key := invalidStrGen.Draw(rt, "key")
			val := rapid.String().Draw(rt, "val")
			labels = map[string]string{key: val}
		} else {
			key := "valid-key"
			val := invalidStrGen.Draw(rt, "val")
			labels = map[string]string{key: val}
		}

		_, err := BuildKnativeService("test", "default", "img", 80, labels, nil)
		require.Error(t, err)
	})
}

func TestBuildKnativeService_Property_AnnotationPreservation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		annotations := rapid.MapOfN(rapid.String(), rapid.String(), 0, 10).Draw(rt, "annotations")

		// Copy annotations to compare later
		expectedAnnos := make(map[string]string)
		for k, v := range annotations {
			expectedAnnos[k] = v
		}

		svc, err := BuildKnativeService("test", "default", "img", 80, nil, annotations)
		require.NoError(t, err)

		// Check all original annotations are present
		for k, v := range expectedAnnos {
			assert.Equal(t, v, svc.Annotations[k])
		}

		// Check ArgoCD and min-scale annotations are always present
		assert.Equal(t, "IgnoreExtraneous", svc.Annotations["argocd.argoproj.io/compare-options"])
		assert.Equal(t, "0", svc.Annotations["autoscaling.knative.dev/min-scale"])
	})
}

func TestBuildKnativeService_Property_LabelPassthrough(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		validKeyGen := rapid.StringMatching(`^[a-z0-9A-Z]([a-z0-9A-Z_-]{0,61}[a-z0-9A-Z])?$`)
		validValGen := rapid.StringMatching(`^([a-z0-9A-Z]([a-z0-9A-Z_-]{0,61}[a-z0-9A-Z])?)?$`)

		labels := rapid.MapOfN(validKeyGen, validValGen, 0, 10).Draw(rt, "labels")

		expectedLabels := make(map[string]string)
		for k, v := range labels {
			expectedLabels[k] = v
		}

		svc, err := BuildKnativeService("test", "default", "img", 80, labels, nil)
		require.NoError(t, err)

		for k, v := range expectedLabels {
			assert.Equal(t, v, svc.Labels[k])
			assert.Equal(t, v, svc.Spec.Template.Labels[k])
		}
	})
}
