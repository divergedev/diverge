package cli

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestCompletionCmd_Shells(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			app := &App{}
			root := NewRootCmd(app)
			var stdout bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stdout)
			root.SetArgs([]string{"completion", shell})

			err := root.Execute()
			require.NoError(t, err)
			assert.NotEmpty(t, stdout.String())
			assert.Contains(t, stdout.String(), "diverge")
		})
	}
}

func TestCompletionCmd_InvalidShell(t *testing.T) {
	app := &App{}
	root := NewRootCmd(app)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"completion", "tcsh"})

	err := root.Execute()
	require.Error(t, err)
}

func TestCompleteEnvironmentNames(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	env1 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-42", Namespace: "default"},
	}
	env2 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-10", Namespace: "default"},
	}
	env3 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "feat-auth", Namespace: "default"},
	}
	envOtherNs := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-99", Namespace: "other"},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(env1, env2, env3, envOtherNs).
		Build()

	app := &App{
		Namespace: "default",
		Client:    c,
	}
	cmd := NewRootCmd(app)

	fn := completeEnvironmentNames(app)
	names, dir := fn(cmd, nil, "pr-")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, dir)
	assert.Equal(t, []string{"pr-10", "pr-42"}, names)

	// Already has 1 arg -> no further completion
	names, dir = fn(cmd, []string{"pr-42"}, "")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, dir)
	assert.Nil(t, names)
}

func TestCompleteAgentTaskNames(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	task1 := &divergeiov1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "fix-flaky-test", Namespace: "default"},
	}
	task2 := &divergeiov1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "fix-auth-bug", Namespace: "default"},
	}
	task3 := &divergeiov1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "upgrade-deps", Namespace: "default"},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(task1, task2, task3).
		Build()

	app := &App{
		Namespace: "default",
		Client:    c,
	}
	cmd := NewRootCmd(app)

	fn := completeAgentTaskNames(app)
	names, dir := fn(cmd, nil, "fix-")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, dir)
	assert.Equal(t, []string{"fix-auth-bug", "fix-flaky-test"}, names)

	// Already has 1 arg -> no further completion
	names, dir = fn(cmd, []string{"fix-auth-bug"}, "")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, dir)
	assert.Nil(t, names)
}
