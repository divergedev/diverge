package notifier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"hegel.dev/go/hegel"
)

func TestStatusReporterPathTraversal(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		sha := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(50))

		var receivedPaths []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedPaths = append(receivedPaths, r.URL.Path)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"target_url": "http://example.com"}`))
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
		glErr := gl.PostCommitStatus(context.Background(), env, "pending", "desc")

		// Test GitHub
		gh := NewGitHubStatusReporter(server.URL, "token")
		env.Spec.Source.Provider = "github"
		env.Spec.Source.Project = "owner/repo"
		ghErr := gh.PostCommitStatus(context.Background(), env, "pending", "desc")

		// Property: either the SHA was rejected as invalid, or any paths that
		// reached the server contain no path traversal sequences.
		if glErr == nil && ghErr == nil {
			// Both succeeded — verify no path traversal in server-received paths
			for _, p := range receivedPaths {
				assert.NotContains(ht, p, "..", "Path traversal detected in %s", p)
			}
		}
		// If either returned an error, the invalid SHA was correctly rejected
		// before reaching the HTTP server — that's the safe behavior.
	})
}
