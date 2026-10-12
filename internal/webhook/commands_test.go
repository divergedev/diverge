package webhook

import (
	"context"
	"testing"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestParseSlashCommand(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantOK  bool
		wantCmd *SlashCommand
	}{
		{
			name:   "fix command with objective",
			body:   "/diverge fix resolve nil pointer in payment handler",
			wantOK: true,
			wantCmd: &SlashCommand{
				Action:    "fix",
				Objective: "resolve nil pointer in payment handler",
			},
		},
		{
			name:   "fix command without objective rejected",
			body:   "/diverge fix   ",
			wantOK: false,
		},
		{
			name:   "preview command",
			body:   "Looks good!\n/diverge preview\nthanks",
			wantOK: true,
			wantCmd: &SlashCommand{
				Action: "preview",
			},
		},
		{
			name:   "teardown command",
			body:   "/diverge teardown",
			wantOK: true,
			wantCmd: &SlashCommand{
				Action: "teardown",
			},
		},
		{
			name:   "extend-ttl with explicit duration",
			body:   "/diverge extend-ttl 48h",
			wantOK: true,
			wantCmd: &SlashCommand{
				Action:   "extend-ttl",
				Duration: 48 * time.Hour,
			},
		},
		{
			name:   "extend-ttl with default duration",
			body:   "/diverge extend-ttl",
			wantOK: true,
			wantCmd: &SlashCommand{
				Action:   "extend-ttl",
				Duration: 24 * time.Hour,
			},
		},
		{
			name:   "extend-ttl with invalid duration rejected",
			body:   "/diverge extend-ttl notaduration",
			wantOK: false,
		},
		{
			name:   "pause command",
			body:   "/diverge pause",
			wantOK: true,
			wantCmd: &SlashCommand{
				Action: "pause",
			},
		},
		{
			name:   "resume command",
			body:   "/diverge resume",
			wantOK: true,
			wantCmd: &SlashCommand{
				Action: "resume",
			},
		},
		{
			name:   "unrelated comment",
			body:   "LGTM! Ship it.",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd, ok := ParseSlashCommand(tc.body)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.NotNil(t, cmd)
				assert.Equal(t, tc.wantCmd.Action, cmd.Action)
				assert.Equal(t, tc.wantCmd.Objective, cmd.Objective)
				assert.Equal(t, tc.wantCmd.Duration, cmd.Duration)
			}
		})
	}
}

func TestExecuteSlashCommand_Lifecycle(t *testing.T) {
	ctx := context.Background()
	c := newTestClient()

	repoURL := "https://github.com/divergedev/demo-payments-api"
	branch := "feat/payments"
	prNumber := 42

	// 1. /diverge fix creates AgentTask
	fixCmd, ok := ParseSlashCommand("/diverge fix fix flaky currency conversion")
	require.True(t, ok)
	require.NoError(t, ExecuteSlashCommand(ctx, c, "default", fixCmd, repoURL, branch, prNumber))

	var taskList v1alpha1.AgentTaskList
	require.NoError(t, c.List(ctx, &taskList, client.InNamespace("default")))
	require.Len(t, taskList.Items, 1)
	task := taskList.Items[0]
	assert.Equal(t, "fix flaky currency conversion", task.Spec.Objective)
	assert.Equal(t, repoURL, task.Spec.Repository.URL)
	assert.Equal(t, branch, task.Spec.Repository.BaseBranch)
	assert.Equal(t, "42", task.Annotations["divergedev.com/pr-number"])
	assert.False(t, task.Spec.Suspended)

	// 2. /diverge pause suspends the AgentTask
	pauseCmd, _ := ParseSlashCommand("/diverge pause")
	require.NoError(t, ExecuteSlashCommand(ctx, c, "default", pauseCmd, repoURL, prNumberStringToBranch(branch), prNumber))
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: task.Name, Namespace: "default"}, &task))
	assert.True(t, task.Spec.Suspended)

	// 3. /diverge resume unsuspends the AgentTask
	resumeCmd, _ := ParseSlashCommand("/diverge resume")
	require.NoError(t, ExecuteSlashCommand(ctx, c, "default", resumeCmd, repoURL, branch, prNumber))
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: task.Name, Namespace: "default"}, &task))
	assert.False(t, task.Spec.Suspended)

	// 4. /diverge preview creates Environment
	previewCmd, _ := ParseSlashCommand("/diverge preview")
	require.NoError(t, ExecuteSlashCommand(ctx, c, "default", previewCmd, repoURL, branch, prNumber))

	envName := previewEnvName("pr", "divergedev/demo-payments-api", prNumber)
	var env v1alpha1.Environment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: envName, Namespace: "default"}, &env))
	assert.Equal(t, branch, env.Spec.Source.Branch)
	assert.Equal(t, prNumber, env.Spec.Source.MR)

	// 5. /diverge extend-ttl updates Environment TTL
	extendCmd, _ := ParseSlashCommand("/diverge extend-ttl 72h")
	require.NoError(t, ExecuteSlashCommand(ctx, c, "default", extendCmd, repoURL, branch, prNumber))
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: envName, Namespace: "default"}, &env))
	require.NotNil(t, env.Spec.Lifecycle.TTL)
	assert.Equal(t, 72*time.Hour, env.Spec.Lifecycle.TTL.Duration)

	// 6. /diverge teardown deletes Environment
	teardownCmd, _ := ParseSlashCommand("/diverge teardown")
	require.NoError(t, ExecuteSlashCommand(ctx, c, "default", teardownCmd, repoURL, branch, prNumber))
	err := c.Get(ctx, types.NamespacedName{Name: envName, Namespace: "default"}, &env)
	assert.True(t, apierrors.IsNotFound(err))

	// Also verify PreviewGroup extend-ttl & teardown
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pg-pr-42",
			Annotations: map[string]string{
				"divergedev.com/pr-number": "42",
			},
		},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				MR: 42,
			},
		},
	}
	require.NoError(t, c.Create(ctx, pg))
	require.NoError(t, ExecuteSlashCommand(ctx, c, "default", extendCmd, repoURL, branch, prNumber))
	var updatedPG v1alpha1.PreviewGroup
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "pg-pr-42"}, &updatedPG))
	require.NotNil(t, updatedPG.Spec.Lifecycle)
	require.NotNil(t, updatedPG.Spec.Lifecycle.TTL)
	assert.Equal(t, 72*time.Hour, updatedPG.Spec.Lifecycle.TTL.Duration)
}

func prNumberStringToBranch(b string) string { return b }
