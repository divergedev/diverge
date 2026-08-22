package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func generateSignature(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestGitHubWebhookHMACValidation(t *testing.T) {
	fakeK8s := newTestClient()

	config := WebhookConfig{SecretToken: "secret"}
	handler := &GitHubWebhookHandler{Client: fakeK8s, Config: config, DefaultNS: "default"}
	body := `{"action": "opened", "pull_request": {"number": 1, "head": {"ref": "feat/test", "sha": "abc123def456"}, "base": {"ref": "main"}}, "repository": {"full_name": "team/repo"}}`
	validSig := generateSignature("secret", body)

	tests := []struct {
		name       string
		signature  string
		statusCode int
	}{
		{
			name:       "valid signature",
			signature:  validSig,
			statusCode: http.StatusOK,
		},
		{
			name:       "invalid signature",
			signature:  "sha256=invalid",
			statusCode: http.StatusUnauthorized,
		},
		{
			name:       "missing signature",
			signature:  "",
			statusCode: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}
			req.Header.Set("X-GitHub-Event", "pull_request")
			if tt.signature != "" {
				req.Header.Set("X-Hub-Signature-256", tt.signature)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.statusCode {
				t.Errorf("Expected status %d, got %d", tt.statusCode, rr.Code)
			}
		})
	}
}

func TestGitHubWebhookMalformedPayload(t *testing.T) {
	config := WebhookConfig{SecretToken: "secret"}
	handler := &GitHubWebhookHandler{Config: config}

	body := `{bad json`
	sig := generateSignature("secret", body)

	req, _ := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", "pull_request")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for malformed json, got %d", rr.Code)
	}
}

func TestGitHubWebhookCreatesEnvironment(t *testing.T) {
	fakeK8s := newTestClient()

	config := WebhookConfig{SecretToken: "secret"}
	handler := &GitHubWebhookHandler{
		Client:    fakeK8s,
		Config:    config,
		DefaultNS: "preview",
	}

	body := `{
		"action": "opened",
		"pull_request": {
			"number": 42,
			"head": {"ref": "feat/new-payments", "sha": "abc123def456789"},
			"base": {"ref": "main"}
		},
		"repository": {"full_name": "divergedev/demo-payments-api"}
	}`
	sig := generateSignature("secret", body)

	req, _ := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", "pull_request")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", rr.Code)
	}

	// Verify CR contents
	env := &divergeiov1alpha1.Environment{}
	err := fakeK8s.Get(context.Background(), client.ObjectKey{Name: "pr-661f4f5e-42", Namespace: "preview"}, env)
	if err != nil {
		t.Fatalf("Expected Environment CR to be created, got %v", err)
	}
	if env.Spec.Source.Provider != "github" {
		t.Errorf("Expected Provider github, got %s", env.Spec.Source.Provider)
	}
	if env.Spec.Source.Project != "divergedev/demo-payments-api" {
		t.Errorf("Expected Project divergedev/demo-payments-api, got %s", env.Spec.Source.Project)
	}
	if env.Spec.Source.MR != 42 {
		t.Errorf("Expected MR 42, got %d", env.Spec.Source.MR)
	}
	if env.Spec.Source.Branch != "feat/new-payments" {
		t.Errorf("Expected Branch feat/new-payments, got %s", env.Spec.Source.Branch)
	}
}

type mockTransport struct {
	roundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTripFunc(req)
}

func TestGitHubWebhookWithConfigFetcher(t *testing.T) {
	fakeK8s := newTestClient()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// editorconfig-checker-disable
		_, _ = w.Write([]byte(`apiVersion: diverge.io/v1alpha1
kind: ServicePreview
metadata:
  name: github-api
spec:
  serviceName: github-api
  namespace: my-namespace
`))
		// editorconfig-checker-enable
	}))
	defer ts.Close()

	configFetcher := &GitHubConfigFetcher{
		Token: "secret-token",
		HTTPClient: &http.Client{
			Transport: &mockTransport{
				roundTripFunc: func(req *http.Request) (*http.Response, error) {
					if req.URL.Query().Get("ref") != "feat/new-payments" {
						t.Errorf("Expected ref feat/new-payments, got %s", req.URL.Query().Get("ref"))
					}
					newURL, err := http.NewRequest(req.Method, ts.URL+req.URL.Path+"?"+req.URL.RawQuery, req.Body)
					if err != nil {
						return nil, err
					}
					newURL.Header = req.Header
					return ts.Client().Do(newURL)
				},
			},
		},
	}

	config := WebhookConfig{SecretToken: "secret"}
	handler := &GitHubWebhookHandler{
		Client:        fakeK8s,
		Config:        config,
		ConfigFetcher: configFetcher,
		DefaultNS:     "preview",
	}

	body := `{
		"action": "opened",
		"pull_request": {
			"number": 42,
			"head": {"ref": "feat/new-payments", "sha": "abc123def456789"},
			"base": {"ref": "main"}
		},
		"repository": {"full_name": "divergedev/demo-payments-api"}
	}`
	sig := generateSignature("secret", body)

	req, _ := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", "pull_request")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", rr.Code)
	}

	env := &divergeiov1alpha1.Environment{}
	err := fakeK8s.Get(context.Background(), client.ObjectKey{Name: "pr-661f4f5e-42", Namespace: "preview"}, env)
	if err != nil {
		t.Fatalf("Expected Environment CR to be created, got %v", err)
	}

	if env.Spec.ServiceConfig == nil {
		t.Fatalf("Expected ServiceConfig to be populated")
	}
	if env.Spec.ServiceConfig.ServiceName != "github-api" {
		t.Errorf("Expected ServiceName github-api, got %s", env.Spec.ServiceConfig.ServiceName)
	}
	if len(env.Spec.Deploy.ChangedServices) != 1 || env.Spec.Deploy.ChangedServices[0] != "github-api" {
		t.Errorf("Expected ChangedServices [github-api], got %v", env.Spec.Deploy.ChangedServices)
	}
}

func TestGitHubWebhookDeletesEnvironment(t *testing.T) {
	existingEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-661f4f5e-42",
			Namespace: "preview",
		},
	}
	fakeK8s := newTestClient(existingEnv)

	config := WebhookConfig{SecretToken: "secret"}
	handler := &GitHubWebhookHandler{
		Client:    fakeK8s,
		Config:    config,
		DefaultNS: "preview",
	}

	body := `{
		"action": "closed",
		"pull_request": {
			"number": 42,
			"head": {"ref": "feat/new-payments", "sha": "abc123def456789"},
			"base": {"ref": "main"}
		},
		"repository": {"full_name": "divergedev/demo-payments-api"}
	}`
	sig := generateSignature("secret", body)

	req, _ := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", "pull_request")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", rr.Code)
	}

	err := fakeK8s.Get(context.Background(), client.ObjectKey{Name: "pr-661f4f5e-42", Namespace: "preview"}, &divergeiov1alpha1.Environment{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("Expected NotFound, got %v", err)
	}
}

type errorClient struct {
	client.Client
}

func (e *errorClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return context.DeadlineExceeded // simulate a generic error
}

func TestGitHubWebhookDeletesEnvironmentError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(scheme)
	fakeK8s := fake.NewClientBuilder().WithScheme(scheme).Build()

	config := WebhookConfig{SecretToken: "secret"}
	handler := &GitHubWebhookHandler{
		Client:    &errorClient{Client: fakeK8s},
		Config:    config,
		DefaultNS: "preview",
	}

	body := `{
		"action": "closed",
		"pull_request": {
			"number": 42,
			"head": {"ref": "feat/new-payments", "sha": "abc123def456789"},
			"base": {"ref": "main"}
		},
		"repository": {"full_name": "divergedev/demo-payments-api"}
	}`
	sig := generateSignature("secret", body)

	req, _ := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", "pull_request")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("Expected 500, got %d", rr.Code)
	}
}
