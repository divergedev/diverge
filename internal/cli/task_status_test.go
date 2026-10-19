package cli

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestTaskStatusCmd(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-task",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationGuidance: "Focus on retry idempotency",
			},
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective:     "Refactor database migrations",
			BudgetUSD:     "10.00",
			MaxIterations: 15,
			Repository: v1alpha1.AgentTaskRepository{
				URL:        "https://github.com/org/repo",
				BaseBranch: "main",
			},
		},
		Status: v1alpha1.AgentTaskStatus{
			Phase:          v1alpha1.AgentTaskPhaseActive,
			SandboxPodName: "sandbox-test-task",
			SandboxIP:      "10.244.0.42",
			Iteration:      4,
			CostUSD:        "1.25",
			PRURL:          "https://github.com/org/repo/pull/99",
			Message:        "Running migration tests",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(task).
		WithStatusSubresource(&v1alpha1.AgentTask{}).
		Build()

	app := &App{
		Namespace: "default",
		Client:    fakeClient,
	}

	r, w, err := os.Pipe()
	require.NoError(t, err)
	origStdout := os.Stdout
	os.Stdout = w
	runErr := runTaskStatus(context.Background(), app, "test-task", "text")
	_ = w.Close()
	os.Stdout = origStdout
	require.NoError(t, runErr)
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Contains(t, string(out), "Iteration:    4 / 15")
	require.Contains(t, string(out), "Cost:         $1.25 / $10.00")
	require.Contains(t, string(out), "Guidance:     Focus on retry idempotency")
	require.Contains(t, string(out), "Sandbox Pod:  sandbox-test-task (IP: 10.244.0.42)")
	require.Contains(t, string(out), "Status Msg:   Running migration tests")

	err = runTaskStatus(context.Background(), app, "test-task", "json")
	require.NoError(t, err)

	err = runTaskStatus(context.Background(), app, "test-task", "yaml")
	require.NoError(t, err)
}
