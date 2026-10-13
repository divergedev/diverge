package main

import (
	"testing"

	"github.com/divergedev/diverge/internal/controller"
	"github.com/divergedev/diverge/internal/notifier"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMainCompiles is a build canary — ensures main.go compiles
// and all wired dependencies resolve. The actual binary is tested
// via the pre-push go-build hook and e2e tests.
func TestMainCompiles(t *testing.T) {
	// This test exists to ensure this package has test coverage.
	// The main() function requires a running Kubernetes cluster,
	// so we only verify compilation here.
	t.Log("cmd/controller compiles successfully")
}

func TestAgentTaskNotifierWiring(t *testing.T) {
	t.Run("defaults to NoopTaskNotifier when provider is noop or token is empty", func(t *testing.T) {
		var taskNotifier controller.TaskNotifier = &controller.NoopTaskNotifier{}
		if tn := notifier.NewTaskNotifier("noop", "some-token", "", nil); tn != nil {
			taskNotifier = tn
		}
		_, isNoop := taskNotifier.(*controller.NoopTaskNotifier)
		assert.True(t, isNoop)

		taskNotifier = &controller.NoopTaskNotifier{}
		if tn := notifier.NewTaskNotifier("github", "", "", nil); tn != nil {
			taskNotifier = tn
		}
		_, isNoop = taskNotifier.(*controller.NoopTaskNotifier)
		assert.True(t, isNoop)
	})

	t.Run("wires SCMTaskNotifier for github and gitlab when token is configured", func(t *testing.T) {
		for _, provider := range []string{"github", "gitlab"} {
			var taskNotifier controller.TaskNotifier = &controller.NoopTaskNotifier{}
			if tn := notifier.NewTaskNotifier(provider, "secret-token", "https://scm.example.com", nil); tn != nil {
				taskNotifier = tn
			}
			scmNotifier, ok := taskNotifier.(*notifier.SCMTaskNotifier)
			require.True(t, ok, "expected *notifier.SCMTaskNotifier for provider %s", provider)
			require.NotNil(t, scmNotifier.Underlying)
		}
	})

	t.Run("resolveAgentTaskNotifier resolves GITHUB_TOKEN and GITLAB_TOKEN when explicit token is empty", func(t *testing.T) {
		t.Setenv("DIVERGE_NOTIFIER_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "gh-env-token")
		t.Setenv("GITHUB_API_URL", "https://ghe.example.com/api/v3")
		tn := resolveAgentTaskNotifier("github", "", nil)
		_, ok := tn.(*notifier.SCMTaskNotifier)
		require.True(t, ok)

		t.Setenv("GITLAB_TOKEN", "gl-env-token")
		t.Setenv("GITLAB_API_URL", "https://gitlab.example.com")
		tnGL := resolveAgentTaskNotifier("gitlab", "", nil)
		_, ok = tnGL.(*notifier.SCMTaskNotifier)
		require.True(t, ok)
	})
}
