package cli

import (
	"context"
	"fmt"
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

	env, err := buildEnvironment(context.Background(), "preview-mr-42", gitCtx, resolved, cfg, app, 42)
	require.NoError(t, err)

	// Verify metadata
	assert.Equal(t, "preview-mr-42", env.Name)
	assert.Equal(t, "diverge-system", env.Namespace)
	assert.Equal(t, "preview-mr-42", env.Labels["divergedev.com/environment"])
	assert.Equal(t, "github", env.Labels["divergedev.com/provider"])
	assert.Equal(t, "42", env.Labels["divergedev.com/mr"])

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

	// Verify banner not set when not configured
	assert.Nil(t, env.Spec.Routing.Banner)
}

func TestBuildEnvironmentWithBanner(t *testing.T) {
	gitCtx := &git.GitContext{
		Provider: "github",
		Project:  "divergedev/diverge",
		Branch:   "feat/banner",
	}

	enabled := true
	resolved := &config.ResolvedSettings{
		EnvironmentSettings: config.EnvironmentSettings{
			Deploy: config.DeploySettings{Mode: "full"},
			Routing: config.RoutingSettings{
				Mode: "header",
				Banner: &config.BannerSettings{
					Enabled:  &enabled,
					Text:     "Staging",
					Position: "bottom",
					Color:    "#00FF00",
				},
			},
		},
	}

	app := &App{Namespace: "default"}

	env, err := buildEnvironment(context.Background(), "preview-mr-1", gitCtx, resolved, nil, app, 1)
	require.NoError(t, err)

	require.NotNil(t, env.Spec.Routing.Banner)
	assert.True(t, env.Spec.Routing.Banner.Enabled)
	assert.Equal(t, "Staging", env.Spec.Routing.Banner.Text)
	assert.Equal(t, "bottom", env.Spec.Routing.Banner.Position)
	assert.Equal(t, "#00FF00", env.Spec.Routing.Banner.Color)
}

func TestBuildEnvironmentWithBannerImplicitEnabled(t *testing.T) {
	gitCtx := &git.GitContext{
		Provider: "github",
		Project:  "divergedev/diverge",
		Branch:   "feat/implicit-banner",
	}

	resolved := &config.ResolvedSettings{
		EnvironmentSettings: config.EnvironmentSettings{
			Deploy: config.DeploySettings{Mode: "full"},
			Routing: config.RoutingSettings{
				Mode: "header",
				Banner: &config.BannerSettings{
					Text:     "Auto Preview",
					Position: "top",
				},
			},
		},
	}

	app := &App{Namespace: "default"}

	env, err := buildEnvironment(context.Background(), "preview-mr-3", gitCtx, resolved, nil, app, 3)
	require.NoError(t, err)

	require.NotNil(t, env.Spec.Routing.Banner)
	assert.True(t, env.Spec.Routing.Banner.Enabled)
	assert.Equal(t, "Auto Preview", env.Spec.Routing.Banner.Text)
	assert.Equal(t, "top", env.Spec.Routing.Banner.Position)
}

func TestBuildEnvironmentWithBannerDisabled(t *testing.T) {
	gitCtx := &git.GitContext{
		Provider: "github",
		Project:  "divergedev/diverge",
		Branch:   "feat/no-banner",
	}

	disabled := false
	resolved := &config.ResolvedSettings{
		EnvironmentSettings: config.EnvironmentSettings{
			Deploy: config.DeploySettings{Mode: "full"},
			Routing: config.RoutingSettings{
				Mode: "header",
				Banner: &config.BannerSettings{
					Enabled: &disabled,
				},
			},
		},
	}

	app := &App{Namespace: "default"}

	env, err := buildEnvironment(context.Background(), "preview-mr-2", gitCtx, resolved, nil, app, 2)
	require.NoError(t, err)

	require.NotNil(t, env.Spec.Routing.Banner)
	assert.False(t, env.Spec.Routing.Banner.Enabled)
}

func TestBuildEnvironmentBannerPositions(t *testing.T) {
	gitCtx := &git.GitContext{
		Provider: "github",
		Project:  "divergedev/diverge",
		Branch:   "feat/banner-pos",
	}
	app := &App{Namespace: "default"}

	for _, pos := range []string{"top", "bottom"} {
		t.Run("valid position "+pos, func(t *testing.T) {
			resolved := &config.ResolvedSettings{
				EnvironmentSettings: config.EnvironmentSettings{
					Deploy: config.DeploySettings{Mode: "full"},
					Routing: config.RoutingSettings{
						Mode: "header",
						Banner: &config.BannerSettings{
							Position: pos,
						},
					},
				},
			}
			env, err := buildEnvironment(context.Background(), "preview-mr-1", gitCtx, resolved, nil, app, 1)
			require.NoError(t, err)
			require.NotNil(t, env.Spec.Routing.Banner)
			assert.Equal(t, pos, env.Spec.Routing.Banner.Position)
		})
	}

	t.Run("invalid position center", func(t *testing.T) {
		resolved := &config.ResolvedSettings{
			EnvironmentSettings: config.EnvironmentSettings{
				Deploy: config.DeploySettings{Mode: "full"},
				Routing: config.RoutingSettings{
					Mode: "header",
					Banner: &config.BannerSettings{
						Position: "center",
					},
				},
			},
		}
		env, err := buildEnvironment(context.Background(), "preview-mr-1", gitCtx, resolved, nil, app, 1)
		require.Error(t, err)
		assert.Nil(t, env)
		assert.Contains(t, err.Error(), `position must be "top" or "bottom", got "center"`)
		assert.Contains(t, err.Error(), "invalid banner configuration")
	})
}

func TestBuildEnvironmentBannerColors(t *testing.T) {
	gitCtx := &git.GitContext{
		Provider: "github",
		Project:  "divergedev/diverge",
		Branch:   "feat/banner-color",
	}
	app := &App{Namespace: "default"}

	for _, color := range []string{"#00FF00", "#FFF"} {
		t.Run("valid color "+color, func(t *testing.T) {
			resolved := &config.ResolvedSettings{
				EnvironmentSettings: config.EnvironmentSettings{
					Deploy: config.DeploySettings{Mode: "full"},
					Routing: config.RoutingSettings{
						Mode: "header",
						Banner: &config.BannerSettings{
							Color: color,
						},
					},
				},
			}
			env, err := buildEnvironment(context.Background(), "preview-mr-1", gitCtx, resolved, nil, app, 1)
			require.NoError(t, err)
			require.NotNil(t, env.Spec.Routing.Banner)
			assert.Equal(t, color, env.Spec.Routing.Banner.Color)
		})
	}

	invalidColors := []string{"green", "12345", "blue; background: red"}
	for _, color := range invalidColors {
		t.Run("invalid color "+color, func(t *testing.T) {
			resolved := &config.ResolvedSettings{
				EnvironmentSettings: config.EnvironmentSettings{
					Deploy: config.DeploySettings{Mode: "full"},
					Routing: config.RoutingSettings{
						Mode: "header",
						Banner: &config.BannerSettings{
							Color: color,
						},
					},
				},
			}
			env, err := buildEnvironment(context.Background(), "preview-mr-1", gitCtx, resolved, nil, app, 1)
			require.Error(t, err)
			assert.Nil(t, env)
			assert.Contains(t, err.Error(), fmt.Sprintf(`color must be a valid hex code (e.g. #00FF00), got %q`, color))
			assert.Contains(t, err.Error(), "invalid banner configuration")
		})
	}
}

func TestBuildEnvironmentBannerInheritedAndOverridden(t *testing.T) {
	gitCtx := &git.GitContext{
		Provider: "github",
		Project:  "divergedev/diverge",
		Branch:   "feat/banner-full",
	}
	app := &App{Namespace: "default"}

	enabledTrue := true
	cfg := &config.Config{
		Version: "1",
		Defaults: config.EnvironmentSettings{
			Deploy: config.DeploySettings{Mode: "full"},
			Routing: config.RoutingSettings{
				Mode: "header",
				Banner: &config.BannerSettings{
					Enabled:  &enabledTrue,
					Text:     "Base Preview",
					Position: "top",
					Color:    "#FF6B00",
				},
			},
		},
		Environments: map[string]config.EnvironmentType{
			"qa": {
				EnvironmentSettings: config.EnvironmentSettings{
					Routing: config.RoutingSettings{
						Banner: &config.BannerSettings{
							Position: "bottom",
							Color:    "#00FF00",
						},
					},
				},
			},
			"staging": {
				EnvironmentSettings: config.EnvironmentSettings{
					Routing: config.RoutingSettings{
						Banner: &config.BannerSettings{
							Text: "Staging Preview",
						},
					},
				},
			},
		},
		LabelOverrides: map[string]config.LabelOverride{
			"theme-light": {
				EnvironmentSettings: config.EnvironmentSettings{
					Routing: config.RoutingSettings{
						Banner: &config.BannerSettings{
							Color: "#FFF",
						},
					},
				},
			},
		},
	}

	t.Run("staging inherits defaults position and color", func(t *testing.T) {
		resolved := cfg.Resolve("staging", nil)
		env, err := buildEnvironment(context.Background(), "staging-env", gitCtx, resolved, cfg, app, 0)
		require.NoError(t, err)
		require.NotNil(t, env.Spec.Routing.Banner)
		assert.True(t, env.Spec.Routing.Banner.Enabled)
		assert.Equal(t, "Staging Preview", env.Spec.Routing.Banner.Text)
		assert.Equal(t, "top", env.Spec.Routing.Banner.Position)
		assert.Equal(t, "#FF6B00", env.Spec.Routing.Banner.Color)
	})

	t.Run("qa overrides position and color while inheriting text and enabled", func(t *testing.T) {
		resolved := cfg.Resolve("qa", nil)
		env, err := buildEnvironment(context.Background(), "qa-env", gitCtx, resolved, cfg, app, 0)
		require.NoError(t, err)
		require.NotNil(t, env.Spec.Routing.Banner)
		assert.True(t, env.Spec.Routing.Banner.Enabled)
		assert.Equal(t, "Base Preview", env.Spec.Routing.Banner.Text)
		assert.Equal(t, "bottom", env.Spec.Routing.Banner.Position)
		assert.Equal(t, "#00FF00", env.Spec.Routing.Banner.Color)
	})

	t.Run("label override updates color", func(t *testing.T) {
		resolved := cfg.Resolve("qa", []string{"theme-light"})
		env, err := buildEnvironment(context.Background(), "qa-label-env", gitCtx, resolved, cfg, app, 0)
		require.NoError(t, err)
		require.NotNil(t, env.Spec.Routing.Banner)
		assert.True(t, env.Spec.Routing.Banner.Enabled)
		assert.Equal(t, "Base Preview", env.Spec.Routing.Banner.Text)
		assert.Equal(t, "bottom", env.Spec.Routing.Banner.Position)
		assert.Equal(t, "#FFF", env.Spec.Routing.Banner.Color)
	})
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

	env, err := buildEnvironment(context.Background(), "staging-main", gitCtx, resolved, nil, app, 0)
	require.NoError(t, err)

	assert.Equal(t, "staging-main", env.Name)
	assert.Equal(t, "full", env.Spec.Deploy.Mode)
	assert.Empty(t, env.Spec.Deploy.ChangedServices)
	assert.Equal(t, "subdomain", env.Spec.Routing.Mode)
	assert.Empty(t, env.Labels["divergedev.com/mr"])
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

	env, err := buildEnvironment(context.Background(), "preview-mr-99", gitCtx, resolved, &config.Config{Version: "1"}, app, 99)
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

	_, err := buildEnvironment(context.Background(), "staging-main", gitCtx, resolved, nil, app, 0)
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
