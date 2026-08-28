package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/divergedev/diverge/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildGraphFromConfig(t *testing.T) {
	cfg := &config.Config{
		Services: map[string]config.ServiceConfig{
			"gateway": {
				Entrypoint: true,
				DependsOn:  []string{"api-router"},
			},
			"api-router": {
				DependsOn: []string{"payments", "orders"},
			},
			"payments": {},
			"orders":   {},
		},
	}

	g := buildGraphFromConfig(cfg)

	eps := g.Entrypoints()
	require.Len(t, eps, 1)
	assert.Equal(t, "gateway", eps[0])

	edges := g.Edges()
	assert.Len(t, edges, 3)
}

func TestValidateConfig_Valid(t *testing.T) {
	app := &App{}
	cmd := newGraphValidateCmd(app)

	tmpFile := filepath.Join(t.TempDir(), ".diverge.yaml")
	err := os.WriteFile(tmpFile, []byte("version: \"1\"\nservices:\n  a:\n    entrypoint: true\n    dependsOn: [\"b\"]\n  b:\n    dependsOn: []\n"), 0644)
	require.NoError(t, err)

	cmd.SetArgs([]string{"--config", tmpFile})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	err = cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "✓ No cycles detected")
	assert.Contains(t, out, "✓ All service references valid")
}

func TestValidateConfig_MissingRef(t *testing.T) {
	app := &App{}
	cmd := newGraphValidateCmd(app)

	tmpFile := filepath.Join(t.TempDir(), ".diverge.yaml")
	err := os.WriteFile(tmpFile, []byte("version: \"1\"\nservices:\n  orders:\n    dependsOn: [\"paymnts\"]\n  payments: {}\n"), 0644)
	require.NoError(t, err)

	cmd.SetArgs([]string{"--config", tmpFile})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	err = cmd.Execute()
	require.Error(t, err)

	out := buf.String()
	assert.Contains(t, out, "service 'orders' depends on unknown service 'paymnts'")
	assert.Contains(t, out, "Did you mean 'payments'?")
}

func TestValidateConfig_Cycle(t *testing.T) {
	app := &App{}
	cmd := newGraphValidateCmd(app)

	tmpFile := filepath.Join(t.TempDir(), ".diverge.yaml")
	err := os.WriteFile(tmpFile, []byte("version: \"1\"\nservices:\n  a:\n    dependsOn: [\"b\"]\n  b:\n    dependsOn: [\"a\"]\n"), 0644)
	require.NoError(t, err)

	cmd.SetArgs([]string{"--config", tmpFile})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	err = cmd.Execute()
	require.Error(t, err)

	out := buf.String()
	assert.Contains(t, out, "cycle detected")
}

func TestValidateConfig_SelfDep(t *testing.T) {
	app := &App{}
	cmd := newGraphValidateCmd(app)

	tmpFile := filepath.Join(t.TempDir(), ".diverge.yaml")
	err := os.WriteFile(tmpFile, []byte("version: \"1\"\nservices:\n  a:\n    dependsOn: [\"a\"]\n"), 0644)
	require.NoError(t, err)

	cmd.SetArgs([]string{"--config", tmpFile})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	err = cmd.Execute()
	require.Error(t, err)

	out := buf.String()
	assert.Contains(t, out, "depends on itself")
}

func TestEditDistance(t *testing.T) {
	tests := []struct {
		s1, s2 string
		want   int
	}{
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 2},
		{"paymnts", "payments", 1},
		{"a", "a", 0},
		{"", "abc", 3},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s-%s", tc.s1, tc.s2), func(t *testing.T) {
			got := editDistance(tc.s1, tc.s2)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSuggestService(t *testing.T) {
	// Already tested implicitly via TestValidateConfig_MissingRef
}

func TestGraphShow_ServiceOutput(t *testing.T) {
	app := &App{}
	cmd := newGraphShowCmd(app)

	tmpFile := filepath.Join(t.TempDir(), ".diverge.yaml")
	configContent := "version: \"1\"\nservices:\n  gateway:\n    entrypoint: true\n    dependsOn: [\"api\"]\n  api:\n    dependsOn: [\"payments\"]\n  payments: {}\n"
	err := os.WriteFile(tmpFile, []byte(configContent), 0644)
	require.NoError(t, err)

	cmd.SetArgs([]string{"--config", tmpFile, "--service", "payments"})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	err = cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Ingress paths to payments")
	assert.Contains(t, out, "2 hops")
	assert.Contains(t, out, "Upstream callers")
	assert.Contains(t, out, "api")
}

func TestCreateWithIngressToStderr(t *testing.T) {
	// SKIP this one for now - needs integration test infrastructure
	t.Skip("needs integration test infrastructure")
}
