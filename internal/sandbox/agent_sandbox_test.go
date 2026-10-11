//go:build !no_sandbox

package sandbox

import (
	"context"
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

func TestBuildSandboxClaimSpec(t *testing.T) {
	// 1. WarmPoolRef
	taskPool := &v1alpha1.AgentTask{
		Spec: v1alpha1.AgentTaskSpec{
			Sandbox: v1alpha1.AgentTaskSandbox{
				PoolRef: "prewarmed-dev",
			},
		},
	}
	specPool := BuildSandboxClaimSpec(taskPool)
	assert.Equal(t, map[string]interface{}{"name": "prewarmed-dev"}, specPool["warmPoolRef"])

	// 2. TemplateRef
	taskTmpl := &v1alpha1.AgentTask{
		Spec: v1alpha1.AgentTaskSpec{
			Sandbox: v1alpha1.AgentTaskSandbox{
				TemplateRef: "heavy-compute",
			},
		},
	}
	specTmpl := BuildSandboxClaimSpec(taskTmpl)
	assert.Equal(t, map[string]interface{}{"name": "heavy-compute"}, specTmpl["sandboxTemplateRef"])

	// 3. Fallback default template with resource limits, NetworkPolicy labels, and task env vars
	taskDefault := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-default-1",
			Namespace: "staging",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective:     "Fix login regression",
			BudgetUSD:     "8.00",
			MaxIterations: 12,
			Repository: v1alpha1.AgentTaskRepository{
				URL:           "https://github.com/org/app",
				BaseBranch:    "develop",
				WorkingBranch: "agent/fix-login",
			},
		},
	}
	specDefault := BuildSandboxClaimSpec(taskDefault)
	assert.Nil(t, specDefault["warmPoolRef"])
	assert.Nil(t, specDefault["sandboxTemplateRef"])

	tmpl, ok := specDefault["template"].(map[string]interface{})
	require.True(t, ok)

	// Verify NetworkPolicy pod selector label is present on template.metadata.labels
	tmplMeta, ok := tmpl["metadata"].(map[string]interface{})
	require.True(t, ok)
	tmplLabels, ok := tmplMeta["labels"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "task-default-1", tmplLabels["divergedev.com/agent-task"])
	assert.Equal(t, "diverge", tmplLabels["app.kubernetes.io/managed-by"])

	tmplSpec, ok := tmpl["spec"].(map[string]interface{})
	require.True(t, ok)
	containers, ok := tmplSpec["containers"].([]interface{})
	require.True(t, ok)
	require.Len(t, containers, 1)

	container := containers[0].(map[string]interface{})
	assert.Equal(t, "agent", container["name"])
	assert.Equal(t, DefaultAgentImage, container["image"])
	res, ok := container["resources"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, map[string]interface{}{"cpu": "500m", "memory": "512Mi"}, res["requests"])
	assert.Equal(t, map[string]interface{}{"cpu": "2", "memory": "2Gi"}, res["limits"])

	// Verify injected task context environment variables
	envList, ok := container["env"].([]interface{})
	require.True(t, ok)
	envMap := make(map[string]string, len(envList))
	for _, e := range envList {
		em := e.(map[string]interface{})
		envMap[em["name"].(string)] = em["value"].(string)
	}
	assert.Equal(t, "task-default-1", envMap["DIVERGE_TASK_NAME"])
	assert.Equal(t, "staging", envMap["DIVERGE_TASK_NAMESPACE"])
	assert.Equal(t, "Fix login regression", envMap["DIVERGE_OBJECTIVE"])
	assert.Equal(t, "https://github.com/org/app", envMap["DIVERGE_REPO_URL"])
	assert.Equal(t, "develop", envMap["DIVERGE_BASE_BRANCH"])
	assert.Equal(t, "agent/fix-login", envMap["DIVERGE_WORKING_BRANCH"])
	assert.Equal(t, "8.00", envMap["DIVERGE_BUDGET_USD"])
	assert.Equal(t, "12", envMap["DIVERGE_MAX_ITERATIONS"])
	assert.Equal(t, "/etc/diverge/token/token", envMap["DIVERGE_TOKEN_FILE"])

	// Assert securityContext & volumes
	secCtx, ok := tmplSpec["securityContext"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, secCtx["runAsNonRoot"])
	assert.Equal(t, int64(10001), secCtx["runAsUser"])

	cSecCtx, ok := container["securityContext"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, false, cSecCtx["allowPrivilegeEscalation"])
	caps, ok := cSecCtx["capabilities"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, []interface{}{"ALL"}, caps["drop"])

	volumes, ok := tmplSpec["volumes"].([]interface{})
	require.True(t, ok)
	assert.Len(t, volumes, 2)
}
