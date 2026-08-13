package notifier

import (
	"context"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPreviewGroupTemplateRendering(t *testing.T) {
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pg",
		},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "my/project",
				MR:       42,
				Branch:   "feature-branch",
			},
			Routing: v1alpha1.PreviewGroupRouting{
				HeaderKey:   "x-preview-env",
				HeaderValue: "42",
				ExternalURL: "https://mr-42.preview.example.com",
			},
			Lifecycle: &v1alpha1.PreviewGroupLifecycle{
				TTL: &metav1.Duration{Duration: 2 * time.Hour},
			},
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{Name: "svc-a", Mode: v1alpha1.ServiceModeImage, Image: "my-image:latest"},
				{Name: "svc-b", Mode: v1alpha1.ServiceModeBaseline},
				{Name: "svc-c|hack", Mode: v1alpha1.ServiceModeLocal, Endpoint: "100.64.0.1:8080"},
			},
		},
		Status: v1alpha1.PreviewGroupStatus{
			Phase:        v1alpha1.PreviewGroupPhaseRunning,
			ServiceCount: 3,
			Services: []v1alpha1.PreviewGroupServiceStatus{
				{Name: "svc-a", Phase: v1alpha1.PhaseRunning, Namespace: "ns-a"},
				{Name: "svc-b", Phase: v1alpha1.PhaseRunning, Namespace: "ns-b"},
				{Name: "svc-c|hack", Phase: v1alpha1.PhaseFailed, Namespace: "ns-c", Reason: "CrashLoopBackOff"},
			},
			ExpiresAt: &metav1.Time{Time: time.Now().Add(2 * time.Hour)},
		},
	}

	data := buildPreviewGroupTemplateData(pg, "")

	tests := []struct {
		name     string
		tmplName string
		tmpl     *template.Template // Need to get access to templates
		contains []string
	}{
		{
			name:     "Created Template",
			tmplName: "pg_created",
			contains: []string{
				"Diverge Preview — Deploying",
				"`test-pg`",
				"`feature-branch`",
				"!42",
				"svc-a | 📦 image",
				"svc-b | ☁️ baseline",
				"svc-c\\|hack | 💻 local",
			},
		},
		{
			name:     "Ready Template",
			tmplName: "pg_ready",
			contains: []string{
				"Diverge Preview — Ready!",
				"✅ Running",
				"3 (2 running)",
				"✅ svc-a",
				"✅ svc-b",
				"❌ svc-c\\|hack",
				"curl -H \"x-preview-env: 42\" https://mr-42.preview.example.com",
			},
		},
		{
			name:     "Failed Template",
			tmplName: "pg_failed",
			contains: []string{
				"Diverge Preview — Failed",
				"❌ Failed",
				"`test-pg`",
				"❌ svc-c\\|hack | Failed | CrashLoopBackOff",
			},
		},
		{
			name:     "Teardown Template",
			tmplName: "pg_teardown",
			contains: []string{
				"Diverge Preview — Destroyed",
				"has been cleaned up",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tmpl *template.Template
			switch tt.tmplName {
			case "pg_created":
				tmpl = pgCreatedTemplate
			case "pg_ready":
				tmpl = pgReadyTemplate
			case "pg_failed":
				tmpl = pgFailedTemplate
			case "pg_teardown":
				tmpl = pgTeardownTemplate
			}

			out, err := renderTemplate(tmpl, data)
			if err != nil {
				t.Fatalf("Failed to render template: %v", err)
			}

			for _, check := range tt.contains {
				if !strings.Contains(out, check) {
					t.Errorf("Template output missing expected string: %s\nOutput:\n%s", check, out)
				}
			}
		})
	}
}

func TestNoopPreviewGroupNotifier(t *testing.T) {
	var n PreviewGroupNotifier = &NoopPreviewGroupNotifier{}
	ctx := context.Background()
	pg := &v1alpha1.PreviewGroup{}

	if err := n.PostGroupCreated(ctx, pg); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if err := n.PostGroupReady(ctx, pg); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if err := n.PostGroupFailed(ctx, pg, "error"); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if err := n.PostGroupTeardown(ctx, pg); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if err := n.UpdateGroupStatus(ctx, pg); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}
