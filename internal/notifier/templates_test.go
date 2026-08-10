package notifier

import (
	"fmt"
	"testing"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRenderCreatedTemplate(t *testing.T) {
	data := TemplateData{
		Name:        "pr-123",
		Branch:      "feature-x",
		Mode:        "ephemeral",
		RoutingMode: "header",
		Services:    []string{"svc1", "svc2"},
		TTL:         "24h",
	}
	res, err := renderTemplate(createdTemplate, data)
	assert.NoError(t, err)
	assert.Contains(t, res, "`pr-123`")
	assert.Contains(t, res, "`feature-x`")
	assert.Contains(t, res, "ephemeral")
	assert.Contains(t, res, "header")
	assert.Contains(t, res, "- ⏳ svc1")
	assert.Contains(t, res, "- ⏳ svc2")
	assert.Contains(t, res, "auto-expire in 24h")
}

func TestRenderReadyTemplate(t *testing.T) {
	tests := []struct {
		name      string
		headerKey string
	}{
		{"default header key", "x-diverge-env"},
		{"custom header key", "x-preview-env"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := TemplateData{
				Name:        "pr-123",
				Branch:      "feature-x",
				Mode:        "ephemeral",
				URL:         "https://example.com/pr",
				NumServices: 2,
				Duration:    "2m",
				Services:    []string{"svc1", "svc2"},
				BaseURL:     "https://api.example.com",
				ExpiryTime:  "2023-01-01T00:00:00Z",
				HeaderKey:   tt.headerKey,
			}
			res, err := renderTemplate(readyTemplate, data)
			assert.NoError(t, err)
			assert.Contains(t, res, "✅ Running")
			assert.Contains(t, res, "[🔗 Open Preview](https://example.com/pr)")
			assert.Contains(t, res, "`pr-123`")
			assert.Contains(t, res, "`feature-x`")
			assert.Contains(t, res, "ephemeral (2 services deployed)")
			assert.Contains(t, res, "- ✅ svc1")
			assert.Contains(t, res, fmt.Sprintf("curl -H \"%s: pr-123\" https://api.example.com", tt.headerKey))
			assert.Contains(t, res, fmt.Sprintf("`%s: pr-123`", tt.headerKey))
			assert.Contains(t, res, "Expires 2023-01-01T00:00:00Z")
		})
	}
}

func TestRenderFailedTemplate(t *testing.T) {
	data := TemplateData{
		Name:   "pr-123",
		Reason: "Pod crashed",
		Conditions: []ConditionData{
			{Icon: "❌", Type: "Ready", Message: "OOMKilled"},
		},
	}
	res, err := renderTemplate(failedTemplate, data)
	assert.NoError(t, err)
	assert.Contains(t, res, "❌ Failed")
	assert.Contains(t, res, "`pr-123`")
	assert.Contains(t, res, "Pod crashed")
	assert.Contains(t, res, "- ❌ Ready: OOMKilled")
	assert.Contains(t, res, "kubectl logs -l app=diverge-controller")
}

func TestRenderTeardownTemplate(t *testing.T) {
	data := TemplateData{
		Name:   "pr-123",
		Reason: "Merged",
	}
	res, err := renderTemplate(teardownTemplate, data)
	assert.NoError(t, err)
	assert.Contains(t, res, "`pr-123`")
	assert.Contains(t, res, "Merged")
}

func TestSanitizeMarkdown(t *testing.T) {
	input := "hello | world `test` <script> @here"
	expected := "hello \\| world \\`test\\` &lt;script&gt; @\u200bhere"
	assert.Equal(t, expected, sanitizeMarkdown(input))
}

func TestSanitizeMarkdownMaliciousInput(t *testing.T) {
	input := "<img src=x onerror=alert(1)> @everyone `rm -rf /` | grep secret"
	expected := "&lt;img src=x onerror=alert(1)&gt; @\u200beveryone \\`rm -rf /\\` \\| grep secret"
	assert.Equal(t, expected, sanitizeMarkdown(input))
}

func TestTemplateWithEmptyBaseURL(t *testing.T) {
	data := TemplateData{
		Name:        "pr-123",
		Branch:      "feature-x",
		Mode:        "ephemeral",
		URL:         "https://example.com/pr",
		NumServices: 2,
		Duration:    "2m",
		Services:    []string{"svc1", "svc2"},
		BaseURL:     "",
		ExpiryTime:  "2023-01-01T00:00:00Z",
	}
	res, err := renderTemplate(readyTemplate, data)
	assert.NoError(t, err)
	assert.NotContains(t, res, "curl -H")
}

// BuildTemplateData helpers
func TestBuildTemplateData(t *testing.T) {
	now := time.Now()
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-env",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source:  v1alpha1.EnvironmentSource{Branch: "main"},
			Deploy:  v1alpha1.EnvironmentDeploy{Mode: "ephemeral", ChangedServices: []string{"s1", "s2"}},
			Routing: v1alpha1.EnvironmentRouting{Mode: "subdomain"},
		},
		Status: v1alpha1.EnvironmentStatus{
			CreatedAt: &metav1.Time{Time: now.Add(-2 * time.Minute)},
			ExpiresAt: &metav1.Time{Time: now.Add(2 * time.Hour)},
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: "True", Message: "All good"},
			},
		},
	}
	data := buildTemplateData(env, "reason")
	assert.Equal(t, "test-env", data.Name)
	assert.Equal(t, "main", data.Branch)
	assert.Equal(t, "ephemeral", data.Mode)
	assert.Equal(t, "subdomain", data.RoutingMode)
	assert.Equal(t, []string{"s1", "s2"}, data.Services)
	assert.Equal(t, "never", data.TTL)
	assert.Equal(t, "", data.URL)
	assert.Equal(t, 2, data.NumServices)
	assert.Contains(t, data.Duration, "m")
	assert.Equal(t, "https://test-env.preview.example.com", data.BaseURL)
	assert.Equal(t, env.Status.ExpiresAt.Format(time.RFC3339), data.ExpiryTime)
	assert.Equal(t, "reason", data.Reason)
	assert.Len(t, data.Conditions, 1)
	assert.Equal(t, "✅", data.Conditions[0].Icon)
}

func TestSanitizeHeaderKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Valid header keys — should pass through unchanged
		{"simple lowercase", "x-diverge-env", "x-diverge-env"},
		{"simple uppercase", "X-Diverge-Env", "X-Diverge-Env"},
		{"all caps", "X-CUSTOM-HEADER", "X-CUSTOM-HEADER"},
		{"single char", "X", "X"},
		{"numeric prefix", "1-header", "1-header"},
		{"all numeric", "12345", "12345"},
		{"alphanumeric mix", "x1y2z3", "x1y2z3"},

		// Shell injection attempts — must fall back to default
		{"double quote injection", `x"; echo PWNED; #`, "x-diverge-env"},
		{"command substitution", "x$(printf PWNED)", "x-diverge-env"},
		{"backtick injection", "x`rm -rf /`", "x-diverge-env"},
		{"single quote injection", "x'; cat /etc/passwd", "x-diverge-env"},
		{"dollar variable", "x$HOME", "x-diverge-env"},
		{"pipe injection", "x | cat /etc/passwd", "x-diverge-env"},
		{"semicolon injection", "x; rm -rf /", "x-diverge-env"},
		{"ampersand injection", "x && echo pwned", "x-diverge-env"},

		// Markdown/rendering injection — must fall back to default
		{"newline injection", "x\necho PWNED", "x-diverge-env"},
		{"carriage return", "x\recho PWNED", "x-diverge-env"},
		{"markdown fence break", "x\n```\necho PWNED\n```", "x-diverge-env"},
		{"html angle brackets", "x<script>alert(1)</script>", "x-diverge-env"},
		{"markdown link", "x](http://evil.com)", "x-diverge-env"},

		// Edge cases — must fall back to default
		{"empty string", "", "x-diverge-env"},
		{"only spaces", "   ", "x-diverge-env"},
		{"starts with hyphen", "-x-header", "x-diverge-env"},
		{"contains underscore", "x_header", "x-diverge-env"},
		{"contains dot", "x.header", "x-diverge-env"},
		{"contains colon", "x:header", "x-diverge-env"},
		{"contains slash", "x/header", "x-diverge-env"},
		{"unicode", "x-héader", "x-diverge-env"},
		{"null byte", "x-header\x00", "x-diverge-env"},
		{"tab character", "x-header\t", "x-diverge-env"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeHeaderKey(tt.input)
			assert.Equal(t, tt.expected, result,
				"sanitizeHeaderKey(%q) should return %q", tt.input, tt.expected)
		})
	}
}

func TestBuildTemplateDataHeaderKeyValidation(t *testing.T) {
	tests := []struct {
		name        string
		headerKey   string
		expectedKey string
	}{
		{"empty uses default", "", "x-diverge-env"},
		{"valid custom key", "x-preview-env", "x-preview-env"},
		{"injection falls back to default", `x"; echo PWNED`, "x-diverge-env"},
		{"backtick falls back to default", "x`rm -rf`", "x-diverge-env"},
		{"command sub falls back to default", "x$(whoami)", "x-diverge-env"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: v1alpha1.EnvironmentSpec{
					Source:  v1alpha1.EnvironmentSource{Branch: "main"},
					Deploy:  v1alpha1.EnvironmentDeploy{Mode: "full"},
					Routing: v1alpha1.EnvironmentRouting{HeaderKey: tt.headerKey},
				},
				Status: v1alpha1.EnvironmentStatus{
					CreatedAt: &metav1.Time{Time: time.Now()},
				},
			}
			data := buildTemplateData(env, "")
			assert.Equal(t, tt.expectedKey, data.HeaderKey,
				"buildTemplateData should sanitize HeaderKey %q to %q", tt.headerKey, tt.expectedKey)
		})
	}
}

func TestMaliciousHeaderKeyInRenderedTemplate(t *testing.T) {
	// End-to-end: verify that a malicious HeaderKey in the CRD cannot
	// produce dangerous output in the rendered Markdown/curl command.
	payloads := []struct {
		name  string
		input string
	}{
		{"command substitution", `$(whoami)`},
		{"backtick execution", "`id`"},
		{"quote escape", `"; curl http://evil.com; #`},
		{"newline and fence", "x\n```\nmalicious\n```"},
	}
	for _, tt := range payloads {
		t.Run(tt.name, func(t *testing.T) {
			env := &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "pr-99"},
				Spec: v1alpha1.EnvironmentSpec{
					Source:  v1alpha1.EnvironmentSource{Branch: "main"},
					Deploy:  v1alpha1.EnvironmentDeploy{Mode: "full"},
					Routing: v1alpha1.EnvironmentRouting{HeaderKey: tt.input},
				},
				Status: v1alpha1.EnvironmentStatus{
					URL:       "https://example.com",
					CreatedAt: &metav1.Time{Time: time.Now()},
				},
			}
			data := buildTemplateData(env, "")
			res, err := renderTemplate(readyTemplate, data)
			require.NoError(t, err)

			// The malicious payload must NOT appear in the rendered output
			assert.NotContains(t, res, tt.input,
				"rendered template must not contain raw malicious input %q", tt.input)
			// The safe default must be used instead
			assert.Contains(t, res, "x-diverge-env",
				"rendered template must use safe default header key")
		})
	}
}
