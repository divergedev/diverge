package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func buildTopTestApp(t *testing.T) *App {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	now := time.Now()
	expires := metav1.NewTime(now.Add(2 * time.Hour))

	activeEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-42-payments",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(now.Add(-30 * time.Minute)),
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: "github",
				Branch:   "feat/payments",
				MR:       42,
			},
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Mode: "schema",
			},
			ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{
				ServiceName: "payments-api",
				Resources: &divergeiov1alpha1.ResourceOverride{
					CPURequest:    "1000m",
					MemoryRequest: "1Gi",
				},
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase:     divergeiov1alpha1.PhaseRunning,
			URL:       "https://pr-42.preview.example.com",
			ExpiresAt: &expires,
			Services:  []string{"payments-api"},
		},
	}

	hibernatedEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-19-idle",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(now.Add(-4 * time.Hour)),
			Annotations: map[string]string{
				"divergedev.com/hibernated": "true",
			},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: "github",
				Branch:   "chore/idle",
				MR:       19,
			},
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Mode: "schema",
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase: divergeiov1alpha1.PhaseRunning,
			URL:   "https://pr-19.preview.example.com",
		},
	}

	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pg-checkout",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(now.Add(-15 * time.Minute)),
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: "github",
				Branch:   "feat/checkout",
			},
			Routing: divergeiov1alpha1.PreviewGroupRouting{
				HeaderKey:   "x-preview-env",
				HeaderValue: "mr-42",
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "checkout-svc"},
				{Name: "payments-api"},
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			Phase:        divergeiov1alpha1.PreviewGroupPhaseRunning,
			ServiceCount: 2,
		},
	}

	agentTask := &divergeiov1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "agent-fix-flaky-test",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute)),
		},
		Spec: divergeiov1alpha1.AgentTaskSpec{
			Objective: "Fix flaky checkout integration test",
			Repository: divergeiov1alpha1.AgentTaskRepository{
				URL:           "https://github.com/org/checkout",
				BaseBranch:    "main",
				WorkingBranch: "agent/fix-flaky-test",
			},
			BudgetUSD:     "5.00",
			MaxIterations: 5,
		},
		Status: divergeiov1alpha1.AgentTaskStatus{
			Phase:          divergeiov1alpha1.AgentTaskPhaseActive,
			Iteration:      2,
			TokensConsumed: 48200,
			CostUSD:        "1.25",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(activeEnv, hibernatedEnv, pg, agentTask).
		Build()

	return &App{
		Namespace: "default",
		NoColor:   true,
		Client:    fakeClient,
	}
}

func TestTopOnceTableOutput(t *testing.T) {
	app := buildTopTestApp(t)
	rootCmd := NewRootCmd(app)

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"top", "--once"})

	err := rootCmd.Execute()
	require.NoError(t, err)

	rendered := out.String()
	// Active env: 1 CPU ($0.035) + 1 GiB ($0.004) + schema DB ($0.015) = $0.054/hr
	// Hibernated env: $0.000/hr
	assert.Contains(t, rendered, "DIVERGE TOP  |  Envs: 2 (1 hibernated)  |  Groups: 1  |  AgentTasks: 1  |  Fleet Burn: $0.054/hr ($39.42/mo)  |  LLM Spend: $1.25")
	assert.Contains(t, rendered, "pr-42-payments")
	assert.Contains(t, rendered, "$0.054/hr")
	assert.Contains(t, rendered, "pr-19-idle")
	assert.Contains(t, rendered, "💤 hibernated")
	assert.Contains(t, rendered, "$0.000/hr")
	assert.Contains(t, rendered, "pg-checkout")
	assert.Contains(t, rendered, "x-preview-env=mr-42")
	assert.Contains(t, rendered, "agent-fix-flaky-test")
	assert.Contains(t, rendered, "2/5")
	assert.Contains(t, rendered, "$1.25 / $5.00")
	assert.Contains(t, rendered, "agent/fix-flaky-test")
}

func TestTopJSONAndYAMLOutput(t *testing.T) {
	app := buildTopTestApp(t)

	// JSON output
	jsonCmd := NewRootCmd(app)
	var jsonOut bytes.Buffer
	jsonCmd.SetOut(&jsonOut)
	jsonCmd.SetArgs([]string{"top", "--once", "-o", "json"})
	require.NoError(t, jsonCmd.Execute())

	var snap TopSnapshot
	require.NoError(t, json.Unmarshal(jsonOut.Bytes(), &snap))
	assert.Equal(t, 2, snap.Summary.TotalEnvironments)
	assert.Equal(t, 1, snap.Summary.ActiveEnvironments)
	assert.Equal(t, 1, snap.Summary.HibernatedEnvironments)
	assert.Equal(t, 1, snap.Summary.PreviewGroups)
	assert.Equal(t, 1, snap.Summary.AgentTasks)
	assert.InDelta(t, 0.054, snap.Summary.HourlyBurnUSD, 0.0001)
	assert.InDelta(t, 1.25, snap.Summary.AgentLLMSpendUSD, 0.0001)

	// YAML output
	yamlCmd := NewRootCmd(app)
	var yamlOut bytes.Buffer
	yamlCmd.SetOut(&yamlOut)
	yamlCmd.SetArgs([]string{"top", "--once", "-o", "yaml"})
	require.NoError(t, yamlCmd.Execute())

	var yamlSnap TopSnapshot
	require.NoError(t, yaml.Unmarshal(yamlOut.Bytes(), &yamlSnap))
	assert.Equal(t, 2, yamlSnap.Summary.TotalEnvironments)
	assert.Len(t, yamlSnap.PreviewGroups, 1)
	assert.Len(t, yamlSnap.AgentTasks, 1)

	// Invalid interval
	badCmd := NewRootCmd(app)
	badCmd.SetArgs([]string{"top", "--once", "-i", "50ms"})
	require.Error(t, badCmd.Execute())
}

func TestTopBaselineDriftState(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	driftedEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-99-drifted",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: "github",
				Branch:   "feat/drift-check",
				MR:       99,
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase: divergeiov1alpha1.PhaseRunning,
			URL:   "https://pr-99.preview.example.com",
			Conditions: []metav1.Condition{
				{
					Type:    "BaselineDriftDetected",
					Status:  metav1.ConditionTrue,
					Reason:  "BaselineUpdated",
					Message: "Baseline configuration changed",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(driftedEnv).
		Build()

	app := &App{
		Namespace: "default",
		NoColor:   true,
		Client:    fakeClient,
	}

	// Table output
	tableCmd := NewRootCmd(app)
	var tableOut bytes.Buffer
	tableCmd.SetOut(&tableOut)
	tableCmd.SetArgs([]string{"top", "--once"})
	require.NoError(t, tableCmd.Execute())
	var driftLine string
	for _, line := range strings.Split(tableOut.String(), "\n") {
		if strings.Contains(line, "pr-99-drifted") {
			driftLine = line
		}
	}
	require.NotEmpty(t, driftLine)
	assert.Contains(t, driftLine, "⚠️ drift")

	// JSON output
	jsonCmd := NewRootCmd(app)
	var jsonOut bytes.Buffer
	jsonCmd.SetOut(&jsonOut)
	jsonCmd.SetArgs([]string{"top", "--once", "-o", "json"})
	require.NoError(t, jsonCmd.Execute())

	var snap TopSnapshot
	require.NoError(t, json.Unmarshal(jsonOut.Bytes(), &snap))
	require.Len(t, snap.Environments, 1)
	assert.True(t, snap.Environments[0].Drifted)
	assert.Equal(t, "⚠️ drift", snap.Environments[0].State)
}
