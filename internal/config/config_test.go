package config

import (
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
			Deploy: DeploySettings{Mode: "delta"},
		},
		Environments: map[string]EnvironmentType{
			"preview": {
				EnvironmentSettings: EnvironmentSettings{
					Deploy: DeploySettings{Mode: "full"},
				},
			},
		},
	}

	res := c.Resolve("preview", nil)
	assert.Equal(t, "full", res.Deploy.Mode)
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
