package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestProvidersListCmd(t *testing.T) {
	app := &App{}
	// Intentionally leave app.Client and app.Clientset nil
	// to prove that the command does not require a kube client.

	tests := []struct {
		name      string
		args      []string
		checkFunc func(t *testing.T, out string)
	}{
		{
			name: "table output",
			args: []string{"providers", "list"},
			checkFunc: func(t *testing.T, out string) {
				assert.Contains(t, out, "--- router ---")
				assert.Contains(t, out, "NAME")
				assert.Contains(t, out, "DESCRIPTION")
				known := []string{"gateway", "direct", "noop", "temporal", "kafka"}
				for _, k := range known {
					assert.Contains(t, out, k)
				}
			},
		},
		{
			name: "json output",
			args: []string{"providers", "list", "-o", "json"},
			checkFunc: func(t *testing.T, out string) {
				var providers []ProviderInfo
				err := json.Unmarshal([]byte(out), &providers)
				require.NoError(t, err)
				require.NotEmpty(t, providers)

				found := false
				for _, p := range providers {
					if p.Name == "gateway" {
						found = true
						break
					}
				}
				assert.True(t, found, "missing known provider 'gateway' in JSON output")
			},
		},
		{
			name: "yaml output",
			args: []string{"providers", "list", "-o", "yaml"},
			checkFunc: func(t *testing.T, out string) {
				var providers []ProviderInfo
				err := yaml.Unmarshal([]byte(out), &providers)
				require.NoError(t, err)
				require.NotEmpty(t, providers)

				found := false
				for _, p := range providers {
					if p.Name == "gateway" {
						found = true
						break
					}
				}
				assert.True(t, found, "missing known provider 'gateway' in YAML output")
			},
		},
		{
			name: "alias plugins",
			args: []string{"plugins", "list"},
			checkFunc: func(t *testing.T, out string) {
				assert.Contains(t, out, "--- router ---")
			},
		},
		{
			name: "alias plugin",
			args: []string{"plugin", "list"},
			checkFunc: func(t *testing.T, out string) {
				assert.Contains(t, out, "--- router ---")
			},
		},
		{
			name: "alias provider",
			args: []string{"provider", "list"},
			checkFunc: func(t *testing.T, out string) {
				assert.Contains(t, out, "--- router ---")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &cobra.Command{Use: "root"}
			root.AddCommand(newProvidersCmd(app))

			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetArgs(tt.args)

			err := root.Execute()
			require.NoError(t, err)

			tt.checkFunc(t, buf.String())
		})
	}
}
