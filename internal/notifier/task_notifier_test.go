package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TaskProvidersDeps() registry.Deps {
	return registry.Deps{}
}

type mockSCMCommentClient struct {
	existingID  int64
	findErr     error
	postErr     error
	updateErr   error
	postedBody  string
	updatedBody string
	updatedID   int64
	project     string
	mrID        int
	postCalls   int
	updateCalls int
}

func (m *mockSCMCommentClient) FindExistingComment(_ context.Context, project string, mrID int, _ string) (int64, error) {
	m.project = project
	m.mrID = mrID
	if m.findErr != nil {
		return 0, m.findErr
	}
	return m.existingID, nil
}

func (m *mockSCMCommentClient) PostComment(_ context.Context, project string, mrID int, body string) (int64, error) {
	m.project = project
	m.mrID = mrID
	m.postCalls++
	m.postedBody = body
	if m.postErr != nil {
		return 0, m.postErr
	}
	m.existingID = 9001
	return m.existingID, nil
}

func (m *mockSCMCommentClient) UpdateComment(_ context.Context, project string, mrID int, commentID int64, body string) error {
	m.project = project
	m.mrID = mrID
	m.updateCalls++
	m.updatedID = commentID
	m.updatedBody = body
	return m.updateErr
}

func TestFormatAgentTaskComment(t *testing.T) {
	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name: "fix-checkout-42",
			Annotations: map[string]string{
				"divergedev.com/preview-url":         "https://preview.example.com",
				"divergedev.com/preview-environment": "agent-preview-fix-checkout-42",
			},
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Fix nil pointer panic in checkout handler",
			Repository: v1alpha1.AgentTaskRepository{
				URL:           "https://github.com/divergedev/diverge.git",
				WorkingBranch: "agent/pr-42-fix-checkout",
			},
		},
		Status: v1alpha1.AgentTaskStatus{
			Phase:          v1alpha1.AgentTaskPhaseActive,
			TokensConsumed: 18450,
			CostUSD:        "0.14",
		},
	}

	t.Run("Running comment includes marker, preview magic link, LLM usage, and slash commands", func(t *testing.T) {
		comment := FormatAgentTaskComment(task, "🚀 Running", "")
		assert.Contains(t, comment, AgentTaskCommentMarker)
		assert.Contains(t, comment, "### 🤖 Diverge Autonomous Agent Task: `fix-checkout-42`")
		assert.Contains(t, comment, "| **Status** | 🚀 Running |")
		assert.Contains(t, comment, "| **Objective** | Fix nil pointer panic in checkout handler |")
		assert.Contains(t, comment, "| **Working Branch** | `agent/pr-42-fix-checkout` |")
		assert.Contains(t, comment, "https://preview.example.com?_diverge_env=agent-preview-fix-checkout-42")
		assert.Contains(t, comment, "https://preview.example.com?_diverge_env=clear")
		assert.Contains(t, comment, "| **LLM Usage** | 18450 tokens / $0.14 |")
		assert.NotContains(t, comment, "| **Failure Reason** |")
		assert.Contains(t, comment, "💡 *Commands: `/diverge pause`, `/diverge resume`, or `/diverge fix <new objective>`*")
	})

	t.Run("Failed comment includes failure reason", func(t *testing.T) {
		comment := FormatAgentTaskComment(task, "❌ Failed", "Sandbox execution timed out after 3600s")
		assert.Contains(t, comment, "| **Status** | ❌ Failed |")
		assert.Contains(t, comment, "| **Failure Reason** | Sandbox execution timed out after 3600s |")
	})
}

func TestExtractTaskProjectAndMR(t *testing.T) {
	tests := []struct {
		name        string
		task        *v1alpha1.AgentTask
		wantProject string
		wantMR      int
	}{
		{
			name: "from annotations and https .git URL",
			task: &v1alpha1.AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"divergedev.com/pr-number": "42",
					},
				},
				Spec: v1alpha1.AgentTaskSpec{
					Repository: v1alpha1.AgentTaskRepository{
						URL: "https://github.com/divergedev/diverge.git",
					},
				},
			},
			wantProject: "divergedev/diverge",
			wantMR:      42,
		},
		{
			name: "from labels and SSH git@ URL",
			task: &v1alpha1.AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"divergedev.com/pr-number": "108",
					},
				},
				Spec: v1alpha1.AgentTaskSpec{
					Repository: v1alpha1.AgentTaskRepository{
						URL: "git@github.com:org/service-api.git",
					},
				},
			},
			wantProject: "org/service-api",
			wantMR:      108,
		},
		{
			name: "from workingBranch pr-<N> and project annotation",
			task: &v1alpha1.AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"divergedev.com/project": "group/sub/project",
					},
				},
				Spec: v1alpha1.AgentTaskSpec{
					Repository: v1alpha1.AgentTaskRepository{
						WorkingBranch: "agent/pr-77-fix-bug",
					},
				},
			},
			wantProject: "group/sub/project",
			wantMR:      77,
		},
		{
			name: "from status PRURL",
			task: &v1alpha1.AgentTask{
				Spec: v1alpha1.AgentTaskSpec{
					Repository: v1alpha1.AgentTaskRepository{
						URL: "https://gitlab.com/acme/platform",
					},
				},
				Status: v1alpha1.AgentTaskStatus{
					PRURL: "https://gitlab.com/acme/platform/-/merge_requests/55",
				},
			},
			wantProject: "acme/platform",
			wantMR:      55,
		},
		{
			name: "missing MR returns 0",
			task: &v1alpha1.AgentTask{
				Spec: v1alpha1.AgentTaskSpec{
					Repository: v1alpha1.AgentTaskRepository{
						URL:           "https://github.com/org/repo",
						WorkingBranch: "agent/feature-branch",
					},
				},
			},
			wantProject: "org/repo",
			wantMR:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proj, mr := extractTaskProjectAndMR(tt.task)
			assert.Equal(t, tt.wantProject, proj)
			assert.Equal(t, tt.wantMR, mr)
		})
	}
}

func TestSCMTaskNotifier_UpsertLifecycle(t *testing.T) {
	ctx := context.Background()
	client := &mockSCMCommentClient{}
	n := NewSCMTaskNotifier(client)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name: "task-123",
			Annotations: map[string]string{
				"divergedev.com/pr-number":   "12",
				"divergedev.com/preview-url": "https://preview.diverge.dev",
			},
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Add rate limiting middleware",
			Repository: v1alpha1.AgentTaskRepository{
				URL:           "https://github.com/divergedev/diverge",
				WorkingBranch: "agent/task-123",
			},
		},
		Status: v1alpha1.AgentTaskStatus{
			TokensConsumed: 4200,
			CostUSD:        "0.03",
		},
	}

	// 1. Started -> posts new comment
	require.NoError(t, n.NotifyTaskStarted(ctx, task))
	assert.Equal(t, 1, client.postCalls)
	assert.Equal(t, 0, client.updateCalls)
	assert.Equal(t, "divergedev/diverge", client.project)
	assert.Equal(t, 12, client.mrID)
	assert.Contains(t, client.postedBody, "🚀 Running")
	assert.Contains(t, client.postedBody, "?_diverge_env=agent-preview-task-123")

	// 2. Completed -> updates existing comment (idempotent upsert)
	task.Status.TokensConsumed = 12800
	task.Status.CostUSD = "0.09"
	require.NoError(t, n.NotifyTaskCompleted(ctx, task))
	assert.Equal(t, 1, client.postCalls)
	assert.Equal(t, 1, client.updateCalls)
	assert.Equal(t, int64(9001), client.updatedID)
	assert.Contains(t, client.updatedBody, "✅ Completed")
	assert.Contains(t, client.updatedBody, "12800 tokens / $0.09")

	// 3. Failed -> updates existing comment with failure reason
	require.NoError(t, n.NotifyTaskFailed(ctx, task, "unit tests failed"))
	assert.Equal(t, 2, client.updateCalls)
	assert.Contains(t, client.updatedBody, "❌ Failed")
	assert.Contains(t, client.updatedBody, "unit tests failed")

	// 4. Task without PR number -> graceful no-op
	noPRTask := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "standalone-task"},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Standalone task",
			Repository: v1alpha1.AgentTaskRepository{
				URL:           "https://github.com/divergedev/diverge",
				WorkingBranch: "feat/no-pr",
			},
		},
	}
	require.NoError(t, n.NotifyTaskStarted(ctx, noPRTask))
	assert.Equal(t, 1, client.postCalls)
	assert.Equal(t, 2, client.updateCalls)
}

func TestSCMTaskNotifier_WithGitHubAndGitLabNotifiers(t *testing.T) {
	ctx := context.Background()

	t.Run("GitHubNotifier FindExistingComment PostComment and UpdateComment", func(t *testing.T) {
		var comments []map[string]any
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(comments)
			case http.MethodPost:
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				comments = append(comments, map[string]any{"id": 501, "body": body["body"]})
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id": 501}`))
			case http.MethodPatch:
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				comments[0]["body"] = body["body"]
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id": 501}`))
			}
		}))
		defer ts.Close()

		gh := NewGitHubNotifier(ts.URL, "gh-token")
		tn := NewSCMTaskNotifier(gh)

		task := &v1alpha1.AgentTask{
			ObjectMeta: metav1.ObjectMeta{Name: "gh-agent"},
			Spec: v1alpha1.AgentTaskSpec{
				Objective: "Implement GitHub PR notifier",
				Repository: v1alpha1.AgentTaskRepository{
					URL:           "https://github.com/org/repo.git",
					WorkingBranch: "pr-19",
				},
			},
		}

		require.NoError(t, tn.NotifyPhaseChange(ctx, task, v1alpha1.AgentTaskPhasePending, v1alpha1.AgentTaskPhaseActive))
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0]["body"], "🚀 Running")

		require.NoError(t, tn.NotifyPhaseChange(ctx, task, v1alpha1.AgentTaskPhaseActive, v1alpha1.AgentTaskPhaseCompleted))
		require.Len(t, comments, 1)
		assert.Contains(t, comments[0]["body"], "✅ Completed")
	})

	t.Run("GitLabNotifier FindExistingComment PostComment and UpdateComment", func(t *testing.T) {
		var notes []map[string]any
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(notes)
			case http.MethodPost:
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				notes = append(notes, map[string]any{"id": 601, "body": body["body"]})
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id": 601}`))
			case http.MethodPut:
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				notes[0]["body"] = body["body"]
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id": 601}`))
			}
		}))
		defer ts.Close()

		gl := NewGitLabNotifier(ts.URL, "gl-token")
		tn := NewSCMTaskNotifier(gl)

		task := &v1alpha1.AgentTask{
			ObjectMeta: metav1.ObjectMeta{Name: "gl-agent"},
			Spec: v1alpha1.AgentTaskSpec{
				Objective: "Implement GitLab MR notifier",
				Repository: v1alpha1.AgentTaskRepository{
					URL:           "git@gitlab.com:group/repo.git",
					WorkingBranch: "pr-23",
				},
			},
			Status: v1alpha1.AgentTaskStatus{
				Message: "Reached budget limit",
			},
		}

		require.NoError(t, tn.NotifyTaskStarted(ctx, task))
		require.Len(t, notes, 1)
		assert.Contains(t, notes[0]["body"], "🚀 Running")

		require.NoError(t, tn.NotifyPhaseChange(ctx, task, v1alpha1.AgentTaskPhaseActive, v1alpha1.AgentTaskPhasePaused))
		require.Len(t, notes, 1)
		assert.Contains(t, notes[0]["body"], "⏸️ Paused")
	})
}

func TestNewTaskNotifier_And_TaskProvidersRegistry(t *testing.T) {
	ctx := context.Background()

	t.Run("NewTaskNotifier returns nil for empty token or unknown provider", func(t *testing.T) {
		assert.Nil(t, NewTaskNotifier("github", "", "", nil))
		assert.Nil(t, NewTaskNotifier("gitlab", "   ", "", nil))
		assert.Nil(t, NewTaskNotifier("noop", "secret-token", "", nil))
		assert.Nil(t, NewTaskNotifier("unknown", "secret-token", "", nil))
	})

	t.Run("NewTaskNotifier constructs GitHub and GitLab task notifiers", func(t *testing.T) {
		ghTN := NewTaskNotifier("GitHub", "gh-tok", "https://api.github.example.com/", nil)
		require.NotNil(t, ghTN)
		ghUnderlying, ok := ghTN.Underlying.(*GitHubNotifier)
		require.True(t, ok)
		assert.Equal(t, "gh-tok", ghUnderlying.Token)
		assert.Equal(t, "https://api.github.example.com", ghUnderlying.BaseURL)

		glTN := NewTaskNotifier("GITLAB", "gl-tok", "", nil)
		require.NotNil(t, glTN)
		glUnderlying, ok := glTN.Underlying.(*GitLabNotifier)
		require.True(t, ok)
		assert.Equal(t, "gl-tok", glUnderlying.Token)
		assert.Equal(t, "https://gitlab.com", glUnderlying.BaseURL)
	})

	t.Run("extractTaskProjectAndMR supports divergedev.com/mr-iid annotation", func(t *testing.T) {
		task := &v1alpha1.AgentTask{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"divergedev.com/provider": "gitlab",
					"divergedev.com/project":  "group/service",
					"divergedev.com/mr-iid":   "88",
				},
			},
		}
		proj, mr := extractTaskProjectAndMR(task)
		assert.Equal(t, "group/service", proj)
		assert.Equal(t, 88, mr)
	})

	t.Run("TaskProviders registry creates noop, github, and gitlab providers", func(t *testing.T) {
		noopProvider, err := TaskProviders.Create("noop", TaskProvidersDeps())
		require.NoError(t, err)
		require.NoError(t, noopProvider.NotifyPhaseChange(ctx, nil, v1alpha1.AgentTaskPhasePending, v1alpha1.AgentTaskPhaseActive))

		t.Setenv("DIVERGE_NOTIFIER_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "env-gh-token")
		t.Setenv("GITHUB_API_URL", "https://gh.internal.example.com")
		ghProvider, err := TaskProviders.Create("github", TaskProvidersDeps())
		require.NoError(t, err)
		_, isSCM := ghProvider.(*SCMTaskNotifier)
		assert.True(t, isSCM)

		t.Setenv("DIVERGE_NOTIFIER_TOKEN", "")
		t.Setenv("DIVERGE_GITLAB_TOKEN", "")
		t.Setenv("GITLAB_TOKEN", "env-gl-token")
		t.Setenv("GITLAB_API_URL", "https://gl.internal.example.com")
		glProvider, err := TaskProviders.Create("gitlab", TaskProvidersDeps())
		require.NoError(t, err)
		_, isSCM = glProvider.(*SCMTaskNotifier)
		assert.True(t, isSCM)
	})
}
