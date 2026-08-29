package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestConfig(t *testing.T, content string) string {
	dir := t.TempDir()
	path := filepath.Join(dir, ".diverge.yaml")
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)
	return path
}

func TestRouteCmd_Linear(t *testing.T) {
	cfg := "version: \"1\"\nservices:\n  gateway:\n    entrypoint: true\n    dependsOn: [\"api\"]\n  api:\n    dependsOn: [\"payments\"]\n  payments: {}\n"
	configPath := writeTestConfig(t, cfg)

	cmd := newRouteCmd(&App{})
	cmd.SetArgs([]string{"payments", "--config", configPath})

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Request routing for \"payments\":")
	assert.Contains(t, out, "gateway")
	assert.Contains(t, out, "api")
	assert.Contains(t, out, "payments")
	assert.Contains(t, out, "2 hops")
}

func TestRouteCmd_Diamond(t *testing.T) {
	cfg := "version: \"1\"\nservices:\n  gateway:\n    entrypoint: true\n    dependsOn: [\"api1\", \"api2\"]\n  api1:\n    dependsOn: [\"payments\"]\n  api2:\n    dependsOn: [\"payments\"]\n  payments: {}\n"
	configPath := writeTestConfig(t, cfg)

	cmd := newRouteCmd(&App{})
	cmd.SetArgs([]string{"payments", "--config", configPath})

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Request routing for \"payments\":")
	assert.Contains(t, out, "api1")
	assert.Contains(t, out, "api2")
}

func TestRouteCmd_Unreachable(t *testing.T) {
	cfg := "version: \"1\"\nservices:\n  gateway:\n    entrypoint: true\n  payments: {}\n"
	configPath := writeTestConfig(t, cfg)

	cmd := newRouteCmd(&App{})
	cmd.SetArgs([]string{"payments", "--config", configPath})

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "unreachable")
}

func TestRouteCmd_GatewayFiltering(t *testing.T) {
	cfg := "version: \"1\"\nservices:\n  gateway1:\n    entrypoint: true\n    dependsOn: [\"payments\"]\n  gateway2:\n    entrypoint: true\n    dependsOn: [\"payments\"]\n  payments: {}\n"
	configPath := writeTestConfig(t, cfg)

	cmd := newRouteCmd(&App{})
	cmd.SetArgs([]string{"payments", "--config", configPath, "--gateway", "gateway1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "gateway1")
	assert.NotContains(t, out, "gateway2")
}

func TestRouteCmd_JSON(t *testing.T) {
	cfg := "version: \"1\"\nservices:\n  gateway:\n    entrypoint: true\n    dependsOn: [\"payments\"]\n  payments: {}\n"
	configPath := writeTestConfig(t, cfg)

	cmd := newRouteCmd(&App{})
	cmd.SetArgs([]string{"payments", "--config", configPath, "--output", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := cmd.Execute()
	require.NoError(t, err)

	var res routeJSONOutput
	err = json.Unmarshal(buf.Bytes(), &res)
	require.NoError(t, err)

	assert.Equal(t, "payments", res.Service)
	assert.Equal(t, "x-diverge-env", res.Header)
	assert.Len(t, res.Paths, 1)
	assert.Equal(t, []string{"gateway", "payments"}, res.Paths[0].Hops)
	assert.Equal(t, 1, res.Paths[0].HopsCount)
}

func TestRouteCmd_Live(t *testing.T) {
	cmd := newRouteCmd(&App{})
	cmd.SetArgs([]string{"payments", "--live"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "--live requires Prometheus configuration in .diverge.yaml")
}
