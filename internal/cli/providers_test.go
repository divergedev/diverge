package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestProvidersListCmd(t *testing.T) {
	app := &App{}
	// Intentionally leave app.Client and app.Clientset nil
	// to prove that the command does not require a kube client.

	t.Run("table output", func(t *testing.T) {
		cmd := newProvidersCmd(app)
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{"list"})

		err := cmd.Execute()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "--- router ---") || !strings.Contains(out, "NAME") || !strings.Contains(out, "DESCRIPTION") {
			t.Errorf("missing headers in output:\n%s", out)
		}

		// Known providers
		known := []string{"gateway", "direct", "noop", "temporal", "kafka"}
		for _, k := range known {
			if !strings.Contains(out, k) {
				t.Errorf("missing known provider %q in output", k)
			}
		}
	})

	t.Run("json output", func(t *testing.T) {
		cmd := newProvidersCmd(app)
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{"list", "-o", "json"})

		err := cmd.Execute()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var providers []ProviderInfo
		if err := json.Unmarshal(buf.Bytes(), &providers); err != nil {
			t.Fatalf("invalid json output: %v", err)
		}

		if len(providers) == 0 {
			t.Errorf("expected non-empty json output")
		}

		// Check for some known providers in JSON
		found := false
		for _, p := range providers {
			if p.Name == "gateway" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing known provider 'gateway' in JSON output")
		}
	})
}
