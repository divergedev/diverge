//go:build !no_temporal

package async

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestTemporalProvisioner_Provision(t *testing.T) {
	p := &TemporalProvisioner{Namespace: "staging"}
	assert.Equal(t, "temporal", p.Name())

	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "pr-42"}}
	route := v1alpha1.AsyncRouteSpec{Protocol: "temporal", Target: "payments"}

	res, err := p.Provision(context.Background(), env, route)
	require.NoError(t, err)
	assert.Equal(t, "payments-pr-42", res.ResolvedTarget)
	assert.Equal(t, "payments-pr-42", res.EnvVars["TEMPORAL_TASK_QUEUE"])
	assert.Equal(t, "staging", res.EnvVars["TEMPORAL_NAMESPACE"])
}

func TestTemporalProvisioner_NoNamespace(t *testing.T) {
	p := &TemporalProvisioner{}
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "pr-42"}}
	route := v1alpha1.AsyncRouteSpec{Protocol: "temporal", Target: "orders"}

	res, err := p.Provision(context.Background(), env, route)
	require.NoError(t, err)
	assert.Equal(t, "orders-pr-42", res.ResolvedTarget)
	assert.Equal(t, "orders-pr-42", res.EnvVars["TEMPORAL_TASK_QUEUE"])
	_, hasNS := res.EnvVars["TEMPORAL_NAMESPACE"]
	assert.False(t, hasNS)
}

func TestTemporalProvisioner_CustomMapping(t *testing.T) {
	p := &TemporalProvisioner{Namespace: "staging"}
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "pr-42"}}
	route := v1alpha1.AsyncRouteSpec{
		Protocol:      "temporal",
		Target:        "payments",
		EnvVarMapping: map[string]string{"MY_QUEUE": "{{ .ResolvedTarget }}"},
	}

	res, err := p.Provision(context.Background(), env, route)
	require.NoError(t, err)
	assert.Equal(t, "payments-pr-42", res.EnvVars["MY_QUEUE"])
	_, hasDefault := res.EnvVars["TEMPORAL_TASK_QUEUE"]
	assert.False(t, hasDefault)
}

func TestTemporalProvisioner_Teardown(t *testing.T) {
	p := &TemporalProvisioner{}
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "pr-42"}}
	route := v1alpha1.AsyncRouteSpec{Protocol: "temporal", Target: "payments"}

	err := p.Teardown(context.Background(), env, route)
	require.NoError(t, err)
}
