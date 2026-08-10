package notifier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"pgregory.net/rapid"
)

func TestStatusReporterPathTraversal(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sha := rapid.String().Draw(t, "sha")

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Path should end with the sha (or its url encoded form) but should not traverse up
			if strings.Contains(r.URL.Path, "..") {
				t.Fatalf("Path traversal detected: %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		// Test GitLab
		gl := NewGitLabStatusReporter(server.URL, "token")
		env := &v1alpha1.Environment{
			Spec: v1alpha1.EnvironmentSpec{
				Source: v1alpha1.EnvironmentSource{
					Provider: "gitlab",
					Project:  "my/project",
				},
			},
			Status: v1alpha1.EnvironmentStatus{
				CommitSHA: sha,
			},
		}
		_ = gl.PostCommitStatus(context.Background(), env, "pending", "desc")

		// Test GitHub
		gh := NewGitHubStatusReporter(server.URL, "token")
		env.Spec.Source.Provider = "github"
		env.Spec.Source.Project = "owner/repo"
		_ = gh.PostCommitStatus(context.Background(), env, "pending", "desc")
	})
}
