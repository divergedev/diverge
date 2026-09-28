package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestTaskCreateDryRunOutput(t *testing.T) {
	app := &App{Namespace: "test-ns"}
	ctx := context.Background()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runTaskCreate(
		ctx,
		app,
		"Add payment API",
		"custom-task-name",
		"https://github.com/org/payments",
		"main",
		"dev-pool",
		"",
		"10.00",
		5,
		[]string{"fast", "smart"},
		true, // dry-run
		"yaml",
	)
	_ = w.Close()
	os.Stdout = oldStdout

	require.NoError(t, err)

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	assert.Contains(t, output, "kind: AgentTask")
	assert.Contains(t, output, "name: custom-task-name")
	assert.Contains(t, output, "objective: Add payment API")
	assert.Contains(t, output, "poolRef: dev-pool")
	assert.Contains(t, output, "budgetUSD: \"10.00\"")
}

func TestTaskCreateExecution(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.AgentTask{}).
		Build()

	app := &App{
		Namespace: "default",
		Client:    fakeClient,
	}

	err := runTaskCreate(
		context.Background(),
		app,
		"Add OAuth authentication",
		"task-oauth",
		"https://github.com/org/repo",
		"main",
		"",
		"",
		"5.00",
		3,
		[]string{"smart"},
		false,
		"text",
	)
	require.NoError(t, err)
}

func TestTaskCreateAutoDetectRepo(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.AgentTask{}).
		Build()

	app := &App{
		Namespace: "default",
		Client:    fakeClient,
	}

	// In current git working tree, git remote origin exists (divergedev/diverge)
	// Testing with empty repoURL should auto-detect from the current repo
	err := runTaskCreate(
		context.Background(),
		app,
		"Add automated test runner",
		"task-autodetect",
		"", // empty repo to trigger detection
		"main",
		"",
		"",
		"5.00",
		3,
		[]string{"fast"},
		false,
		"text",
	)
	require.NoError(t, err)

	task := &v1alpha1.AgentTask{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-autodetect"}, task)
	require.NoError(t, err)
	assert.NotEmpty(t, task.Spec.Repository.URL)
}

func TestTaskCreateWithAgentRepoConfig(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.AgentTask{}).
		Build()

	app := &App{
		Namespace: "default",
		Client:    fakeClient,
	}

	tempDir, err := os.MkdirTemp("", "task-create-cfg-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tempDir) }()

	origWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tempDir))
	defer func() { _ = os.Chdir(origWd) }()

	require.NoError(t, runTaskInit(false))

	err = runTaskCreate(
		context.Background(),
		app,
		"Test task from repo config",
		"task-from-cfg",
		"https://github.com/org/repo",
		"main", // default should remain main
		"",     // empty poolRef
		"",     // empty templateRef
		"5.00", // default budget flag should pick up repo config if customized
		5,      // default iterations
		[]string{"fast", "smart"},
		false,
		"text",
	)
	require.NoError(t, err)

	task := &v1alpha1.AgentTask{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "task-from-cfg"}, task)
	require.NoError(t, err)
	assert.Equal(t, "5.00", task.Spec.BudgetUSD)
	assert.Equal(t, int32(5), task.Spec.MaxIterations)
	assert.Contains(t, task.Spec.Capabilities, "claude-3-5-sonnet")
}
