package async

import (
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"pgregory.net/rapid"
)

// Property: NoopProvisioner always returns <target>-<envName> as resolved target
func TestNoopProvisionerProperty_TargetFormat(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		envName := rapid.StringMatching(`^[a-z][a-z0-9-]{0,20}[a-z0-9]$`).Draw(t, "envName")
		target := rapid.StringMatching(`^[a-z][a-z0-9-]{0,20}[a-z0-9]$`).Draw(t, "target")
		protocol := rapid.SampledFrom([]string{"temporal", "kafka"}).Draw(t, "protocol")

		p := &NoopProvisioner{}
		env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: envName}}
		route := v1alpha1.AsyncRouteSpec{Protocol: protocol, Target: target}

		res, err := p.Provision(t.Context(), env, route)
		require.NoError(t, err)

		// Property 1: resolved target is always <target>-<envName>
		require.Equal(t, target+"-"+envName, res.ResolvedTarget)

		// Property 2: default env var is always populated for known protocols
		expectedVar := v1alpha1.DefaultEnvVarForProtocol(protocol)
		require.NotEmpty(t, expectedVar)
		require.Equal(t, res.ResolvedTarget, res.EnvVars[expectedVar])
	})
}

// Property: DefaultEnvVarForProtocol returns non-empty only for known protocols
func TestDefaultEnvVarProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		protocol := rapid.String().Draw(t, "protocol")

		result := v1alpha1.DefaultEnvVarForProtocol(protocol)
		switch protocol {
		case "temporal":
			require.Equal(t, "TEMPORAL_TASK_QUEUE", result)
		case "kafka":
			require.Equal(t, "KAFKA_CONSUMER_GROUP", result)
		default:
			require.Empty(t, result, "unknown protocol must return empty")
		}
	})
}
