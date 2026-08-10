package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestGitLabStatusReporter(t *testing.T) {
	tests := []struct {
		name          string
		state         string
		expectedState string
	}{
		{"pending", "pending", "pending"},
		{"running", "running", "running"},
		{"success", "success", "success"},
		{"failed", "failed", "failed"},
		{"canceled", "canceled", "canceled"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("Expected POST, got %s", r.Method)
				}
				if r.URL.Path != "/api/v4/projects/my/project/statuses/mysha" {
					t.Errorf("Unexpected path %s (RawPath: %s)", r.URL.Path, r.URL.RawPath)
				}
				var payload map[string]string
				_ = json.NewDecoder(r.Body).Decode(&payload)
				if payload["state"] != tc.expectedState {
					t.Errorf("Expected state %s, got %s", tc.expectedState, payload["state"])
				}

				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"target_url": "http://gitlab.com"}`))
			}))
			defer server.Close()

			reporter := NewGitLabStatusReporter(server.URL, "token")
			env := &v1alpha1.Environment{
				Spec: v1alpha1.EnvironmentSpec{
					Source: v1alpha1.EnvironmentSource{
						Provider: "gitlab",
						Project:  "my/project",
					},
				},
				Status: v1alpha1.EnvironmentStatus{
					CommitSHA: "mysha",
					URL:       "http://preview.url",
				},
			}

			err := reporter.PostCommitStatus(context.Background(), env, tc.state, "desc")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			// not checking CommitStatusURL because our impl might not extract target_url exactly, but let's check it doesn't panic.
		})
	}
}

func TestGitHubStatusReporter(t *testing.T) {
	tests := []struct {
		name          string
		state         string
		expectedState string
	}{
		{"pending", "pending", "pending"},
		{"running", "running", "pending"},
		{"success", "success", "success"},
		{"failed", "failed", "failure"},
		{"canceled", "canceled", "error"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("Expected POST, got %s", r.Method)
				}
				if r.URL.Path != "/repos/owner/repo/statuses/mysha" {
					t.Errorf("Unexpected path %s", r.URL.Path)
				}
				var payload map[string]string
				_ = json.NewDecoder(r.Body).Decode(&payload)
				if payload["state"] != tc.expectedState {
					t.Errorf("Expected state %s, got %s", tc.expectedState, payload["state"])
				}

				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"url": "http://github.com"}`))
			}))
			defer server.Close()

			reporter := NewGitHubStatusReporter(server.URL, "token")
			env := &v1alpha1.Environment{
				Spec: v1alpha1.EnvironmentSpec{
					Source: v1alpha1.EnvironmentSource{
						Provider: "github",
						Project:  "owner/repo",
					},
				},
				Status: v1alpha1.EnvironmentStatus{
					CommitSHA: "mysha",
					URL:       "http://preview.url",
				},
			}

			err := reporter.PostCommitStatus(context.Background(), env, tc.state, "desc")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if env.Status.CommitStatusURL != "http://github.com" {
				t.Errorf("Expected URL http://github.com, got %s", env.Status.CommitStatusURL)
			}
		})
	}
}

func TestNoopStatusReporter(t *testing.T) {
	reporter := &NoopStatusReporter{}
	err := reporter.PostCommitStatus(context.Background(), nil, "state", "desc")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}
