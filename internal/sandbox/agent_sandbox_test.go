package sandbox

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
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

func TestRegistryHasProviders(t *testing.T) {
	list := pkgsandbox.Providers.List()
	assert.Contains(t, list, "noop")
	assert.Contains(t, list, "none")
	assert.Contains(t, list, "agent-sandbox")

	p, err := pkgsandbox.Providers.Create("noop", registry.Deps{})
	require.NoError(t, err)
	assert.NotNil(t, p)
}

func TestAgentSandboxProvider(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	p := NewAgentSandboxProvider(fakeClient, nil, scheme)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-abc",
			Namespace: "default",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Add payment gateway",
			Repository: v1alpha1.AgentTaskRepository{
				URL: "https://github.com/org/payments",
			},
			Sandbox: v1alpha1.AgentTaskSandbox{
				PoolRef: "custom-pool",
			},
		},
	}

	res, err := p.Provision(ctx, task)
	require.NoError(t, err)
	assert.Equal(t, "sb-claim-task-abc", res.ClaimName)

	// In fake client without Agent Sandbox controller running, claim has no status yet
	status, err := p.Status(ctx, task)
	require.NoError(t, err)
	assert.Equal(t, "Pending", status.Phase)
	assert.False(t, status.Ready)

	err = p.Teardown(ctx, task)
	assert.NoError(t, err)
}
