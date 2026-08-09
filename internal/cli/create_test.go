package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divergedev/diverge/internal/config"
	"github.com/divergedev/diverge/internal/git"
)

func TestGenerateEnvName(t *testing.T) {
	tests := []struct {
		name     string
		envType  string
		mr       int
		branch   string
		expected string
	}{
		{
			name:     "with MR number",
			envType:  "preview",
			mr:       42,
			branch:   "feat/my-feature",
			expected: "preview-mr-42",
		},
		{
			name:     "without MR uses branch slug",
			envType:  "preview",
			mr:       0,
			branch:   "feat/my-feature",
			expected: "preview-feat-my-feature",
		},
		{
			name:     "qa environment type",
			envType:  "qa",
			mr:       7,
			branch:   "fix/bug",
			expected: "qa-mr-7",
		},
		{
			name:     "staging from main",
			envType:  "staging",
			mr:       0,
			branch:   "main",
			expected: "staging-main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateEnvName(tt.envType, tt.mr, tt.branch)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestBuildEnvironment(t *testing.T) {
	gitCtx := &git.GitContext{
		Provider:  "github",
		Project:   "divergedev/diverge",
		Branch:    "feat/preview-envs",
		RemoteURL: "git@github.com:divergedev/diverge.git",
	}

	resolved := &config.ResolvedSettings{
		EnvironmentSettings: config.EnvironmentSettings{
			Deploy: config.DeploySettings{Mode: "delta"},
			Routing: config.RoutingSettings{
				Mode:      "header",
				HeaderKey: "x-diverge-env",
				Domain:    "preview.example.com",
			},
			Database: config.DatabaseSettings{
				Mode:          "shared",
				ConnectionRef: "staging-db",
			},
			Lifecycle: config.LifecycleSettings{
				TTL: "72h",
			},
		},
	}

	cfg := &config.Config{
		Version: "1",
		Services: map[string]config.ServiceConfig{
			"api": {Paths: []string{"services/api/**"}},
			"web": {Paths: []string{"apps/web/**"}},
		},
	}

	app := &App{Namespace: "diverge-system"}

	env, err := buildEnvironment("preview-mr-42", gitCtx, resolved, cfg, app, 42)
	require.NoError(t, err)

	// Verify metadata
	assert.Equal(t, "preview-mr-42", env.Name)
	assert.Equal(t, "diverge-system", env.Namespace)
	assert.Equal(t, "preview-mr-42", env.Labels["diverge.dev/environment"])
	assert.Equal(t, "github", env.Labels["diverge.dev/provider"])
	assert.Equal(t, "42", env.Labels["diverge.dev/mr"])

	// Verify source
	assert.Equal(t, "github", env.Spec.Source.Provider)
	assert.Equal(t, "divergedev/diverge", env.Spec.Source.Project)
	assert.Equal(t, "feat/preview-envs", env.Spec.Source.Branch)
	assert.Equal(t, 42, env.Spec.Source.MR)

	// Verify deploy
	assert.Equal(t, "delta", env.Spec.Deploy.Mode)
	assert.ElementsMatch(t, []string{"api", "web"}, env.Spec.Deploy.ChangedServices)

	// Verify routing
	assert.Equal(t, "header", env.Spec.Routing.Mode)
	assert.Equal(t, "x-diverge-env", env.Spec.Routing.HeaderKey)
	assert.Equal(t, "preview-mr-42", env.Spec.Routing.HeaderValue)
	assert.Equal(t, "https://preview-mr-42.preview.example.com", env.Spec.Routing.ExternalURL)

	// Verify database
	assert.Equal(t, "shared", env.Spec.Database.Mode)
	assert.Equal(t, "staging-db", env.Spec.Database.ConnectionRef)

	// Verify lifecycle
	assert.NotNil(t, env.Spec.Lifecycle.TTL)
}

func TestBuildEnvironmentNilConfig(t *testing.T) {
	gitCtx := &git.GitContext{
		Provider: "gitlab",
		Project:  "org/repo",
		Branch:   "main",
	}

	resolved := &config.ResolvedSettings{
		EnvironmentSettings: config.EnvironmentSettings{
			Deploy:  config.DeploySettings{Mode: "full"},
			Routing: config.RoutingSettings{Mode: "subdomain"},
		},
	}

	app := &App{Namespace: "default"}

	env, err := buildEnvironment("staging-main", gitCtx, resolved, nil, app, 0)
	require.NoError(t, err)

	assert.Equal(t, "staging-main", env.Name)
	assert.Equal(t, "full", env.Spec.Deploy.Mode)
	assert.Empty(t, env.Spec.Deploy.ChangedServices)
	assert.Equal(t, "subdomain", env.Spec.Routing.Mode)
	assert.Empty(t, env.Labels["diverge.dev/mr"])
}

func TestBuildEnvironmentLabelOverrides(t *testing.T) {
	gitCtx := &git.GitContext{
		Provider: "gitlab",
		Project:  "invenero/engineering/patient-insights",
		Branch:   "feat/new-api",
	}

	cleanupTrue := true
	resolved := &config.ResolvedSettings{
		EnvironmentSettings: config.EnvironmentSettings{
			Deploy: config.DeploySettings{Mode: "full"},
			Routing: config.RoutingSettings{
				Mode: "header",
			},
			Database: config.DatabaseSettings{
				Mode: "fresh",
			},
			Lifecycle: config.LifecycleSettings{
				TTL:            "48h",
				CleanupOnMerge: &cleanupTrue,
			},
		},
	}

	app := &App{Namespace: "preview"}

	env, err := buildEnvironment("preview-mr-99", gitCtx, resolved, &config.Config{Version: "1"}, app, 99)
	require.NoError(t, err)

	assert.Equal(t, "full", env.Spec.Deploy.Mode)
	assert.Equal(t, "fresh", env.Spec.Database.Mode)
	assert.True(t, env.Spec.Lifecycle.CleanupOnMerge)
}

func TestBuildEnvironmentInvalidTTL(t *testing.T) {
	gitCtx := &git.GitContext{
		Provider: "github",
		Project:  "org/repo",
		Branch:   "main",
	}

	resolved := &config.ResolvedSettings{
		EnvironmentSettings: config.EnvironmentSettings{
			Deploy: config.DeploySettings{Mode: "full"},
			Lifecycle: config.LifecycleSettings{
				TTL: "72 hours", // invalid format
			},
		},
	}

	app := &App{Namespace: "default"}

	_, err := buildEnvironment("test", gitCtx, resolved, nil, app, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid TTL")
}

func TestGenerateEnvNameTruncation(t *testing.T) {
	// envType prefix + slug should be truncated to 63 chars
	longBranch := "feat/this-is-a-very-long-branch-name-that-exceeds-the-kubernetes-label-limit"
	name := generateEnvName("preview", 0, longBranch)
	assert.LessOrEqual(t, len(name), 63)
	assert.NotEmpty(t, name)
	// Should not end with a hyphen
	assert.NotEqual(t, '-', name[len(name)-1])
}
