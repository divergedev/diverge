package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitLabConfigFetcher(t *testing.T) {
	t.Run("Happy path", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v4/projects/team/repo/repository/files/.diverge.yaml/raw" {
				t.Errorf("Unexpected path: %s", r.URL.Path)
			}
			if r.URL.Query().Get("ref") != "feat/branch" {
				t.Errorf("Unexpected ref: %s", r.URL.Query().Get("ref"))
			}
			if r.Header.Get("PRIVATE-TOKEN") != "secret" {
				t.Errorf("Unexpected token: %s", r.Header.Get("PRIVATE-TOKEN"))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("service_name: payment-api"))
		}))
		defer ts.Close()

		f := &GitLabConfigFetcher{
			BaseURL:    ts.URL,
			Token:      "secret",
			HTTPClient: ts.Client(),
		}

		data, err := f.FetchConfig(context.Background(), "gitlab", "team/repo", "feat/branch")
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if string(data) != "service_name: payment-api" {
			t.Errorf("Expected config data, got %s", string(data))
		}
	})

	t.Run("404 not found", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		f := &GitLabConfigFetcher{
			BaseURL:    ts.URL,
			Token:      "secret",
			HTTPClient: ts.Client(),
		}

		_, err := f.FetchConfig(context.Background(), "gitlab", "team/repo", "feat/branch")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("Expected not found error, got %v", err)
		}
	})

	t.Run("500 error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		f := &GitLabConfigFetcher{
			BaseURL:    ts.URL,
			Token:      "secret",
			HTTPClient: ts.Client(),
		}

		_, err := f.FetchConfig(context.Background(), "gitlab", "team/repo", "feat/branch")
		if err == nil || !strings.Contains(err.Error(), "unexpected status 500") {
			t.Fatalf("Expected 500 error, got %v", err)
		}
	})
}

func TestGitHubConfigFetcher(t *testing.T) {
	t.Run("Happy path", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/repos/divergedev/repo/contents/.diverge.yaml" {
				t.Errorf("Unexpected path: %s", r.URL.Path)
			}
			if r.URL.Query().Get("ref") != "main" {
				t.Errorf("Unexpected ref: %s", r.URL.Query().Get("ref"))
			}
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Errorf("Unexpected token: %s", r.Header.Get("Authorization"))
			}
			if r.Header.Get("Accept") != "application/vnd.github.raw+json" {
				t.Errorf("Unexpected accept: %s", r.Header.Get("Accept"))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("service_name: my-app"))
		}))
		defer ts.Close()

		// Temporarily replace GitHub API URL by mocking the http roundtripper since GitHubConfigFetcher hardcodes https://api.github.com
		// Actually, wait, GitHubConfigFetcher hardcodes it in apiURL.
		// Let me just inject the test URL for GitHubConfigFetcher.
		// But GitHubConfigFetcher doesn't have a BaseURL.
		// I will just use httptest but I need to intercept the request at the transport level.
		f := &GitHubConfigFetcher{
			Token: "secret",
			HTTPClient: &http.Client{
				Transport: &testTransport{url: ts.URL, client: ts.Client()},
			},
		}

		data, err := f.FetchConfig(context.Background(), "github", "divergedev/repo", "main")
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if string(data) != "service_name: my-app" {
			t.Errorf("Expected config data, got %s", string(data))
		}
	})

	t.Run("404 not found", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		f := &GitHubConfigFetcher{
			Token: "secret",
			HTTPClient: &http.Client{
				Transport: &testTransport{url: ts.URL, client: ts.Client()},
			},
		}

		_, err := f.FetchConfig(context.Background(), "github", "divergedev/repo", "main")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("Expected not found error, got %v", err)
		}
	})
}

type testTransport struct {
	url    string
	client *http.Client
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite https://api.github.com to the mock server URL
	newURL, _ := http.NewRequest(req.Method, t.url+req.URL.Path+"?"+req.URL.RawQuery, req.Body)
	newURL.Header = req.Header
	return t.client.Do(newURL)
}
