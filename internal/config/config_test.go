package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func projectRoot() string {
	_, f, _, _ := runtime.Caller(0)
	// internal/config/config_test.go → project root is ../../
	return filepath.Join(filepath.Dir(f), "..", "..")
}

func TestLoadValidConfig(t *testing.T) {
	path := filepath.Join(projectRoot(), "config", "samples", "diverge.yaml")
	c, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, "1", c.Version)
	assert.Len(t, c.Services, 4)
	assert.NotNil(t, c.Services["patient-api"])
	assert.Equal(t, "delta", c.Defaults.Deploy.Mode)
	assert.Equal(t, "same", c.Defaults.Deploy.Namespace)
	assert.Equal(t, "preview.patient-insights.example.com", c.Defaults.Routing.Domain)
	assert.NotNil(t, c.Environments["preview"])
	assert.Equal(t, "gitlab", c.Notifications.Provider)
}

func TestLoadMinimalConfig(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "diverge.yaml")
	err := os.WriteFile(path, []byte(`version: "1"`), 0644)
	require.NoError(t, err)

	c, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "1", c.Version)
}

func TestLoadMissingFile(t *testing.T) {
	c, err := Load("/does/not/exist.yaml")
	assert.Error(t, err)
	assert.Nil(t, c)
}

func TestLoadInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "diverge.yaml")
	err := os.WriteFile(path, []byte(`version: "1"\ninvalid:`), 0644)
	require.NoError(t, err)

	c, err := Load(path)
	assert.Error(t, err)
	assert.Nil(t, c)
}

func TestResolvePreviewDefaults(t *testing.T) {
	c := &Config{
		Defaults: EnvironmentSettings{
			Deploy: DeploySettings{Mode: "delta", Namespace: "same"},
		},
		Environments: map[string]EnvironmentType{
			"preview": {
				EnvironmentSettings: EnvironmentSettings{
					Deploy: DeploySettings{Mode: "full", Namespace: "create"},
				},
			},
		},
	}

	res := c.Resolve("preview", nil)
	assert.Equal(t, "full", res.Deploy.Mode)
	assert.Equal(t, "create", res.Deploy.Namespace)
}

func TestResolveLabelOverrides(t *testing.T) {
	c := &Config{
		Defaults: EnvironmentSettings{
			Deploy: DeploySettings{Mode: "delta"},
		},
		LabelOverrides: map[string]LabelOverride{
			"diverge/full-stack": {
				EnvironmentSettings: EnvironmentSettings{
					Deploy: DeploySettings{Mode: "full"},
				},
			},
		},
	}

	res := c.Resolve("preview", []string{"diverge/full-stack"})
	assert.Equal(t, "full", res.Deploy.Mode)
}

func TestResolveUnknownEnvType(t *testing.T) {
	c := &Config{
		Defaults: EnvironmentSettings{
			Deploy: DeploySettings{Mode: "delta"},
		},
	}

	res := c.Resolve("unknown", nil)
	assert.Equal(t, "delta", res.Deploy.Mode)
}

func TestResolveMultipleLabels(t *testing.T) {
	c := &Config{
		Defaults: EnvironmentSettings{
			Database: DatabaseSettings{Mode: "shared"},
		},
		LabelOverrides: map[string]LabelOverride{
			"label1": {
				EnvironmentSettings: EnvironmentSettings{
					Database: DatabaseSettings{Mode: "schema"},
				},
			},
			"label2": {
				EnvironmentSettings: EnvironmentSettings{
					Database: DatabaseSettings{Mode: "fresh"},
				},
			},
		},
	}

	// last label wins
	res := c.Resolve("preview", []string{"label1", "label2"})
	assert.Equal(t, "fresh", res.Database.Mode)
}

func TestResolveBannerSettings(t *testing.T) {
	enabledTrue := true
	enabledFalse := false
	c := &Config{
		Defaults: EnvironmentSettings{
			Routing: RoutingSettings{
				Banner: &BannerSettings{
					Enabled:  &enabledTrue,
					Text:     "Default Banner",
					Position: "top",
					Color:    "#FF6B00",
				},
			},
		},
		Environments: map[string]EnvironmentType{
			"staging": {
				EnvironmentSettings: EnvironmentSettings{
					Routing: RoutingSettings{
						Banner: &BannerSettings{
							Text: "Staging Preview",
						},
					},
				},
			},
			"silent": {
				EnvironmentSettings: EnvironmentSettings{
					Routing: RoutingSettings{
						Banner: &BannerSettings{
							Enabled: &enabledFalse,
						},
					},
				},
			},
		},
	}

	stagingRes := c.Resolve("staging", nil)
	require.NotNil(t, stagingRes.Routing.Banner)
	assert.True(t, *stagingRes.Routing.Banner.Enabled)
	assert.Equal(t, "Staging Preview", stagingRes.Routing.Banner.Text)
	assert.Equal(t, "top", stagingRes.Routing.Banner.Position)
	assert.Equal(t, "#FF6B00", stagingRes.Routing.Banner.Color)

	silentRes := c.Resolve("silent", nil)
	require.NotNil(t, silentRes.Routing.Banner)
	assert.False(t, *silentRes.Routing.Banner.Enabled)
	assert.Equal(t, "Default Banner", silentRes.Routing.Banner.Text)
}

func TestBannerSettings_ValidatePosition(t *testing.T) {
	validPositions := []string{"top", "bottom", ""}
	for _, pos := range validPositions {
		b := &BannerSettings{Position: pos}
		assert.NoError(t, b.Validate(), "expected position %q to be valid", pos)
	}

	invalidPositions := []string{"center", "middle", "left", "right", "TOP", "BOTTOM", " top"}
	for _, pos := range invalidPositions {
		b := &BannerSettings{Position: pos}
		err := b.Validate()
		require.Error(t, err, "expected position %q to be invalid", pos)
		assert.Equal(t, fmt.Sprintf("position must be \"top\" or \"bottom\", got %q", pos), err.Error())
	}
}

func TestBannerSettings_ValidateColor(t *testing.T) {
	validColors := []string{"#00FF00", "#FFF", "#fff", "#000", "#123456", "#aBcDeF", ""}
	for _, color := range validColors {
		b := &BannerSettings{Color: color}
		assert.NoError(t, b.Validate(), "expected color %q to be valid", color)
	}

	invalidColors := []string{"green", "12345", "blue; background: red", "#12", "#1234", "#12345", "#1234567", "#GGG", "#ff", " #00FF00"}
	for _, color := range invalidColors {
		b := &BannerSettings{Color: color}
		err := b.Validate()
		require.Error(t, err, "expected color %q to be invalid", color)
		assert.Equal(t, fmt.Sprintf("color must be a valid hex code (e.g. #00FF00), got %q", color), err.Error())
	}
}

func TestBannerSettings_ValidateNil(t *testing.T) {
	var b *BannerSettings
	assert.NoError(t, b.Validate())
}

func TestConfig_Validate(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := &Config{
			Defaults: EnvironmentSettings{
				Routing: RoutingSettings{
					Banner: &BannerSettings{
						Position: "top",
						Color:    "#00FF00",
					},
				},
			},
			Environments: map[string]EnvironmentType{
				"staging": {
					EnvironmentSettings: EnvironmentSettings{
						Routing: RoutingSettings{
							Banner: &BannerSettings{
								Position: "bottom",
								Color:    "#FFF",
							},
						},
					},
				},
			},
			LabelOverrides: map[string]LabelOverride{
				"custom": {
					EnvironmentSettings: EnvironmentSettings{
						Routing: RoutingSettings{
							Banner: &BannerSettings{
								Position: "top",
								Color:    "#123456",
							},
						},
					},
				},
			},
		}
		assert.NoError(t, cfg.Validate())
	})

	t.Run("invalid defaults banner position", func(t *testing.T) {
		cfg := &Config{
			Defaults: EnvironmentSettings{
				Routing: RoutingSettings{
					Banner: &BannerSettings{
						Position: "center",
					},
				},
			},
		}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "defaults.routing.banner")
		assert.Contains(t, err.Error(), `position must be "top" or "bottom", got "center"`)
	})

	t.Run("invalid environment banner color", func(t *testing.T) {
		cfg := &Config{
			Environments: map[string]EnvironmentType{
				"staging": {
					EnvironmentSettings: EnvironmentSettings{
						Routing: RoutingSettings{
							Banner: &BannerSettings{
								Color: "green",
							},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "environments[staging].routing.banner")
		assert.Contains(t, err.Error(), `color must be a valid hex code (e.g. #00FF00), got "green"`)
	})

	t.Run("invalid label override banner", func(t *testing.T) {
		cfg := &Config{
			LabelOverrides: map[string]LabelOverride{
				"diverge/bad": {
					EnvironmentSettings: EnvironmentSettings{
						Routing: RoutingSettings{
							Banner: &BannerSettings{
								Color: "12345",
							},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "label_overrides[diverge/bad].routing.banner")
		assert.Contains(t, err.Error(), `color must be a valid hex code (e.g. #00FF00), got "12345"`)
	})

	t.Run("nil config", func(t *testing.T) {
		var cfg *Config
		assert.NoError(t, cfg.Validate())
	})
}

func TestResolveBannerSettings_InheritanceAndOverrides(t *testing.T) {
	enabledTrue := true
	enabledFalse := false

	cfg := &Config{
		Defaults: EnvironmentSettings{
			Routing: RoutingSettings{
				Banner: &BannerSettings{
					Enabled:  &enabledTrue,
					Text:     "Base Preview",
					Position: "top",
					Color:    "#00FF00",
				},
			},
		},
		Environments: map[string]EnvironmentType{
			"qa": {
				EnvironmentSettings: EnvironmentSettings{
					Routing: RoutingSettings{
						Banner: &BannerSettings{
							Position: "bottom",
							Color:    "#FF0000",
						},
					},
				},
			},
			"partial": {
				EnvironmentSettings: EnvironmentSettings{
					Routing: RoutingSettings{
						Banner: &BannerSettings{
							Text: "Partial Override",
						},
					},
				},
			},
		},
		LabelOverrides: map[string]LabelOverride{
			"label/override-color": {
				EnvironmentSettings: EnvironmentSettings{
					Routing: RoutingSettings{
						Banner: &BannerSettings{
							Color: "#0000FF",
						},
					},
				},
			},
			"label/disable-banner": {
				EnvironmentSettings: EnvironmentSettings{
					Routing: RoutingSettings{
						Banner: &BannerSettings{
							Enabled: &enabledFalse,
						},
					},
				},
			},
		},
	}

	t.Run("inherits defaults when unconfigured in env", func(t *testing.T) {
		res := cfg.Resolve("preview", nil)
		require.NotNil(t, res.Routing.Banner)
		assert.True(t, *res.Routing.Banner.Enabled)
		assert.Equal(t, "Base Preview", res.Routing.Banner.Text)
		assert.Equal(t, "top", res.Routing.Banner.Position)
		assert.Equal(t, "#00FF00", res.Routing.Banner.Color)
		assert.NoError(t, res.Validate())
	})

	t.Run("inherits unchanged fields on partial override", func(t *testing.T) {
		res := cfg.Resolve("partial", nil)
		require.NotNil(t, res.Routing.Banner)
		assert.True(t, *res.Routing.Banner.Enabled)
		assert.Equal(t, "Partial Override", res.Routing.Banner.Text)
		assert.Equal(t, "top", res.Routing.Banner.Position)
		assert.Equal(t, "#00FF00", res.Routing.Banner.Color)
		assert.NoError(t, res.Validate())
	})

	t.Run("overrides position and color per environment", func(t *testing.T) {
		res := cfg.Resolve("qa", nil)
		require.NotNil(t, res.Routing.Banner)
		assert.True(t, *res.Routing.Banner.Enabled)
		assert.Equal(t, "Base Preview", res.Routing.Banner.Text)
		assert.Equal(t, "bottom", res.Routing.Banner.Position)
		assert.Equal(t, "#FF0000", res.Routing.Banner.Color)
		assert.NoError(t, res.Validate())
	})

	t.Run("label override updates color", func(t *testing.T) {
		res := cfg.Resolve("qa", []string{"label/override-color"})
		require.NotNil(t, res.Routing.Banner)
		assert.True(t, *res.Routing.Banner.Enabled)
		assert.Equal(t, "Base Preview", res.Routing.Banner.Text)
		assert.Equal(t, "bottom", res.Routing.Banner.Position)
		assert.Equal(t, "#0000FF", res.Routing.Banner.Color)
		assert.NoError(t, res.Validate())
	})

	t.Run("label override disables banner", func(t *testing.T) {
		res := cfg.Resolve("qa", []string{"label/disable-banner"})
		require.NotNil(t, res.Routing.Banner)
		assert.False(t, *res.Routing.Banner.Enabled)
		assert.Equal(t, "Base Preview", res.Routing.Banner.Text)
		assert.Equal(t, "bottom", res.Routing.Banner.Position)
		assert.Equal(t, "#FF0000", res.Routing.Banner.Color)
		assert.NoError(t, res.Validate())
	})
}

func TestAtlasSettings_ParseAndResolve(t *testing.T) {
	yamlContent := "version: \"1\"\n" +
		"defaults:\n" +
		"  database:\n" +
		"    mode: schema\n" +
		"    atlas:\n" +
		"      mode: versioned\n" +
		"      engine: job\n" +
		"      migration_config_map: base-migrations\n" +
		"      blocking: true\n" +
		"      policy:\n" +
		"        destructive: error\n" +
		"environments:\n" +
		"  qa:\n" +
		"    database:\n" +
		"      atlas:\n" +
		"        engine: operator\n" +
		"        policy:\n" +
		"          destructive: warn\n" +
		"  declarative:\n" +
		"    database:\n" +
		"      atlas:\n" +
		"        mode: declarative\n" +
		"        schema_config_map: my-schema\n" +
		"        policy:\n" +
		"          destructive: allow\n" +
		"label_overrides:\n" +
		"  \"db/custom-image\":\n" +
		"    database:\n" +
		"      atlas:\n" +
		"        image: custom/atlas:v1\n"
	cfg, err := Parse([]byte(yamlContent))
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	t.Run("inherits defaults", func(t *testing.T) {
		res := cfg.Resolve("preview", nil)
		require.NotNil(t, res.Database.Atlas)
		assert.Equal(t, "versioned", res.Database.Atlas.Mode)
		assert.Equal(t, "job", res.Database.Atlas.Engine)
		assert.Equal(t, "base-migrations", res.Database.Atlas.MigrationConfigMap)
		assert.True(t, *res.Database.Atlas.Blocking)
		assert.Equal(t, "error", res.Database.Atlas.Policy.Destructive)
	})

	t.Run("environment override", func(t *testing.T) {
		res := cfg.Resolve("qa", nil)
		require.NotNil(t, res.Database.Atlas)
		assert.Equal(t, "versioned", res.Database.Atlas.Mode)
		assert.Equal(t, "operator", res.Database.Atlas.Engine)
		assert.Equal(t, "base-migrations", res.Database.Atlas.MigrationConfigMap)
		assert.Equal(t, "warn", res.Database.Atlas.Policy.Destructive)
	})

	t.Run("declarative mode override", func(t *testing.T) {
		res := cfg.Resolve("declarative", nil)
		require.NotNil(t, res.Database.Atlas)
		assert.Equal(t, "declarative", res.Database.Atlas.Mode)
		assert.Equal(t, "my-schema", res.Database.Atlas.SchemaConfigMap)
		assert.Equal(t, "allow", res.Database.Atlas.Policy.Destructive)
	})

	t.Run("label override image", func(t *testing.T) {
		res := cfg.Resolve("qa", []string{"db/custom-image"})
		require.NotNil(t, res.Database.Atlas)
		assert.Equal(t, "custom/atlas:v1", res.Database.Atlas.Image)
		assert.Equal(t, "operator", res.Database.Atlas.Engine)
	})
}

func TestAtlasSettings_Validation(t *testing.T) {
	t.Run("invalid mode", func(t *testing.T) {
		yamlContent := "version: \"1\"\ndefaults:\n  database:\n    atlas:\n      mode: invalid-mode\n"
		cfg, err := Parse([]byte(yamlContent))
		require.NoError(t, err)
		assert.ErrorContains(t, cfg.Validate(), "atlas mode must be")
	})

	t.Run("invalid engine", func(t *testing.T) {
		yamlContent := "version: \"1\"\ndefaults:\n  database:\n    atlas:\n      engine: invalid-engine\n"
		cfg, err := Parse([]byte(yamlContent))
		require.NoError(t, err)
		assert.ErrorContains(t, cfg.Validate(), "atlas engine must be")
	})

	t.Run("invalid policy destructive", func(t *testing.T) {
		yamlContent := "version: \"1\"\ndefaults:\n  database:\n    atlas:\n      policy:\n        destructive: invalid-policy\n"
		cfg, err := Parse([]byte(yamlContent))
		require.NoError(t, err)
		assert.ErrorContains(t, cfg.Validate(), "atlas policy destructive must be")
	})
}
