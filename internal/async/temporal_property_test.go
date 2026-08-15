//go:build !no_temporal

package async

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var k8sChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
var k8sAlpha = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z"}

func genName(ht *hegel.T, minLen, maxLen int) string {
	first := hegel.Draw(ht, hegel.SampledFrom(k8sAlpha))
	midLen := hegel.Draw(ht, hegel.Integers(maxOf(0, minLen-2), maxLen-2))
	mid := ""
	for i := 0; i < midLen; i++ {
		mid += hegel.Draw(ht, hegel.SampledFrom(k8sChars))
	}
	last := hegel.Draw(ht, hegel.SampledFrom(k8sChars))
	return first + mid + last
}

func maxOf(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func TestTemporalProperty_FormatAndEnvVars(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		envName := genName(ht, 2, 15)
		target := genName(ht, 2, 15)
		ns := hegel.Draw(ht, hegel.Text().MaxSize(20))
		customEnv := hegel.Draw(ht, hegel.Integers(0, 1))

		var envVarMapping map[string]string
		if customEnv == 1 {
			envVarMapping = map[string]string{
				"CUSTOM_TASK_QUEUE": "",
			}
		}

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: envName},
		}
		route := v1alpha1.AsyncRouteSpec{
			Target:        target,
			EnvVarMapping: envVarMapping,
		}

		p := &TemporalProvisioner{Namespace: ns}
		result, err := p.Provision(context.Background(), env, route)
		if err != nil {
			ht.Fatalf("Provision failed: %v", err)
		}

		// 1. Target format property
		require.Equal(ht, target+"-"+envName, result.ResolvedTarget)

		// 2, 3, 4. EnvVar properties
		if customEnv == 1 {
			require.NotContains(ht, result.EnvVars, "TEMPORAL_TASK_QUEUE")
			require.NotContains(ht, result.EnvVars, "TEMPORAL_NAMESPACE")
			require.Contains(ht, result.EnvVars, "CUSTOM_TASK_QUEUE")
		} else {
			require.Contains(ht, result.EnvVars, "TEMPORAL_TASK_QUEUE")
			require.Equal(ht, target+"-"+envName, result.EnvVars["TEMPORAL_TASK_QUEUE"])

			if ns != "" {
				require.Contains(ht, result.EnvVars, "TEMPORAL_NAMESPACE")
				require.Equal(ht, ns, result.EnvVars["TEMPORAL_NAMESPACE"])
			} else {
				require.NotContains(ht, result.EnvVars, "TEMPORAL_NAMESPACE")
			}
		}
	})
}
