//go:build !no_knative

package knative

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
)

func genString(ht *hegel.T, chars []string, min, max int) string {
	length := hegel.Draw(ht, hegel.Integers(min, max))
	if length == 0 {
		return ""
	}
	res := ""
	for i := 0; i < length; i++ {
		res += hegel.Draw(ht, hegel.SampledFrom(chars))
	}
	return res
}

func genDNS1123(ht *hegel.T) string {
	chars := []string{"a", "b", "0", "1", "-"}
	first := hegel.Draw(ht, hegel.SampledFrom([]string{"a", "b", "0", "1"}))
	length := hegel.Draw(ht, hegel.Integers(0, 8))
	if length == 0 {
		return first
	}
	res := first
	for i := 0; i < length-1; i++ {
		res += hegel.Draw(ht, hegel.SampledFrom(chars))
	}
	res += hegel.Draw(ht, hegel.SampledFrom([]string{"a", "b", "0", "1"}))
	return res
}

func genLabelValue(ht *hegel.T) string {
	length := hegel.Draw(ht, hegel.Integers(0, 10))
	if length == 0 {
		return ""
	}
	first := hegel.Draw(ht, hegel.SampledFrom([]string{"a", "b", "0", "1"}))
	if length == 1 {
		return first
	}
	res := first
	chars := []string{"a", "b", "0", "1", "-", ".", "_"}
	for i := 0; i < length-2; i++ {
		res += hegel.Draw(ht, hegel.SampledFrom(chars))
	}
	res += hegel.Draw(ht, hegel.SampledFrom([]string{"a", "b", "0", "1"}))
	return res
}

func TestBuildKnativeService_Property_ValidLabelsAccepted(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		numLabels := hegel.Draw(ht, hegel.Integers(0, 10))
		labels := make(map[string]string)
		for i := 0; i < numLabels; i++ {
			k := genDNS1123(ht)
			v := genLabelValue(ht)
			labels[k] = v
		}

		svc, err := BuildKnativeService("test", "default", "img", 80, labels, nil)
		require.NoError(ht, err)
		require.NotNil(ht, svc)
	})
}

func TestBuildKnativeService_Property_InvalidLabelsRejected(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		// Either the key is invalid, or the value is invalid
		invalidKey := hegel.Draw(ht, hegel.SampledFrom([]bool{true, false}))

		var labels map[string]string
		if invalidKey {
			key := genString(ht, []string{"!", "@", "#", " ", "\\n"}, 1, 10)
			val := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(20))
			labels = map[string]string{key: val}
		} else {
			key := "valid-key"
			val := genString(ht, []string{"!", "@", "#", " ", "\\n"}, 1, 10)
			labels = map[string]string{key: val}
		}

		_, err := BuildKnativeService("test", "default", "img", 80, labels, nil)
		require.Error(ht, err)
	})
}

func TestBuildKnativeService_Property_AnnotationPreservation(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		numAnnos := hegel.Draw(ht, hegel.Integers(0, 10))
		annotations := make(map[string]string)
		for i := 0; i < numAnnos; i++ {
			k := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(20))
			v := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(20))
			annotations[k] = v
		}

		// Copy annotations to compare later
		expectedAnnos := make(map[string]string)
		for k, v := range annotations {
			expectedAnnos[k] = v
		}

		svc, err := BuildKnativeService("test", "default", "img", 80, nil, annotations)
		require.NoError(ht, err)

		// Check all original annotations are present
		for k, v := range expectedAnnos {
			assert.Equal(ht, v, svc.Annotations[k])
		}

		// Check ArgoCD and min-scale annotations are always present
		assert.Equal(ht, "IgnoreExtraneous", svc.Annotations["argocd.argoproj.io/compare-options"])
		assert.Equal(ht, "0", svc.Annotations["autoscaling.knative.dev/min-scale"])
	})
}

func TestBuildKnativeService_Property_LabelPassthrough(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		numLabels := hegel.Draw(ht, hegel.Integers(0, 10))
		labels := make(map[string]string)
		for i := 0; i < numLabels; i++ {
			k := genDNS1123(ht)
			v := genLabelValue(ht)
			labels[k] = v
		}

		expectedLabels := make(map[string]string)
		for k, v := range labels {
			expectedLabels[k] = v
		}

		svc, err := BuildKnativeService("test", "default", "img", 80, labels, nil)
		require.NoError(ht, err)

		for k, v := range expectedLabels {
			assert.Equal(ht, v, svc.Labels[k])
			assert.Equal(ht, v, svc.Spec.Template.Labels[k])
		}
	})
}
