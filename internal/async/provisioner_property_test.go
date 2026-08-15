package async

import (
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var firstChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z"}
var midChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "-"}
var lastChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}

func genProvisionerTargetName(ht *hegel.T) string {
	first := hegel.Draw(ht, hegel.SampledFrom(firstChars))
	length := hegel.Draw(ht, hegel.Integers(0, 20))
	if length == 0 {
		return first + hegel.Draw(ht, hegel.SampledFrom(lastChars))
	}
	rest := ""
	for i := 0; i < length-1; i++ {
		rest += hegel.Draw(ht, hegel.SampledFrom(midChars))
	}
	return first + rest + hegel.Draw(ht, hegel.SampledFrom(lastChars))
}

// Property: NoopProvisioner always returns <target>-<envName> as resolved target
func TestNoopProvisionerProperty_TargetFormat(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		envName := genProvisionerTargetName(ht)
		target := genProvisionerTargetName(ht)
		protocol := hegel.Draw(ht, hegel.SampledFrom([]string{"temporal", "kafka"}))

		p := &NoopProvisioner{}
		env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: envName}}
		route := v1alpha1.AsyncRouteSpec{Protocol: protocol, Target: target}

		res, err := p.Provision(t.Context(), env, route)
		require.NoError(ht, err)

		// Property 1: resolved target is always <target>-<envName>
		require.Equal(ht, target+"-"+envName, res.ResolvedTarget)

		// Property 2: default env var is always populated for known protocols
		expectedVar := v1alpha1.DefaultEnvVarForProtocol(protocol)
		require.NotEmpty(ht, expectedVar)
		require.Equal(ht, res.ResolvedTarget, res.EnvVars[expectedVar])
	})
}

// Property: DefaultEnvVarForProtocol returns non-empty only for known protocols
func TestDefaultEnvVarProperty(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		protocol := hegel.Draw(ht, hegel.Text())

		result := v1alpha1.DefaultEnvVarForProtocol(protocol)
		switch protocol {
		case "temporal":
			require.Equal(ht, "TEMPORAL_TASK_QUEUE", result)
		case "kafka":
			require.Equal(ht, "KAFKA_CONSUMER_GROUP", result)
		default:
			require.Empty(ht, result, "unknown protocol must return empty")
		}
	})
}
