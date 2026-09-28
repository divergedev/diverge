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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestTaskCommandsRegistration(t *testing.T) {
	app := &App{}
	cmd := newTaskCmd(app)
	require.NotNil(t, cmd)

	subcommands := make(map[string]bool)
	for _, sc := range cmd.Commands() {
		subcommands[sc.Name()] = true
	}

	assert.True(t, subcommands["create"])
	assert.True(t, subcommands["status"])
	assert.True(t, subcommands["list"])
	assert.True(t, subcommands["logs"])
	assert.True(t, subcommands["delete"])
	assert.True(t, subcommands["pause"])
	assert.True(t, subcommands["resume"])
}

func TestTaskCreateDryRun(t *testing.T) {
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

func TestTaskLifecycleCLI(t *testing.T) {
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
	ctx := context.Background()

	// 1. Create task
	err := runTaskCreate(
		ctx,
		app,
		"Implement oauth",
		"task-cli-test",
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

	// 2. Status task
	err = runTaskStatus(ctx, app, "task-cli-test", "text")
	require.NoError(t, err)

	// 3. List tasks
	err = runTaskList(ctx, app, "table")
	require.NoError(t, err)

	// 4. Pause task
	err = runTaskSetSuspended(ctx, app, "task-cli-test", true)
	require.NoError(t, err)

	// Verify suspended flag
	task := &v1alpha1.AgentTask{}
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "task-cli-test"}, task)
	require.NoError(t, err)
	assert.True(t, task.Spec.Suspended)

	// 5. Resume task
	err = runTaskSetSuspended(ctx, app, "task-cli-test", false)
	require.NoError(t, err)

	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "task-cli-test"}, task)
	require.NoError(t, err)
	assert.False(t, task.Spec.Suspended)

	// 6. Delete task
	err = runTaskDelete(ctx, app, "task-cli-test")
	require.NoError(t, err)
}
