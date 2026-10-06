package sandbox

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

func TestNoopSandboxProvider(t *testing.T) {
	ctx := context.Background()
	p := &NoopSandboxProvider{}

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-task",
			Namespace: "default",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Test noop provider",
		},
	}

	res, err := p.Provision(ctx, task)
	require.NoError(t, err)
	assert.True(t, res.Ready)
	assert.Equal(t, "noop-claim-test-task", res.ClaimName)

	status, err := p.Status(ctx, task)
	require.NoError(t, err)
	assert.True(t, status.Ready)
	assert.Equal(t, "Running", status.Phase)

	rc, err := p.StreamLogs(ctx, task, pkgsandbox.LogOptions{})
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	out, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Contains(t, string(out), "Noop sandbox agent started")

	err = p.Teardown(ctx, task)
	assert.NoError(t, err)
}
