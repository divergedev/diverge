package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockConfigFetcher struct {
	configData map[string][]byte
}

func (m *mockConfigFetcher) FetchConfig(ctx context.Context, provider, project, ref string) ([]byte, error) {
	key := fmt.Sprintf("%s:%s:%s", provider, project, ref)
	if data, ok := m.configData[key]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("config not found")
}

func TestGitLabPreviewGroupWebhookHandler_ServeHTTP(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	tests := []struct {
		name           string
		method         string
		token          string
		secretToken    string
		payload        GitLabMRPayload
		configData     map[string][]byte
		expectedStatus int
		verifyState    func(t *testing.T, c client.Client)
	}{
		{
			name:           "Method Not Allowed",
			method:         http.MethodGet,
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "Unauthorized",
			method:         http.MethodPost,
			token:          "wrong-token",
			secretToken:    "secret",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:        "MR Open Event",
			method:      http.MethodPost,
			token:       "secret",
			secretToken: "secret",
			payload: GitLabMRPayload{
				ObjectKind: "merge_request",
				ObjectAttributes: struct {
					ID           int    `json:"id"`
					IID          int    `json:"iid"`
					TargetBranch string `json:"target_branch"`
					SourceBranch string `json:"source_branch"`
					State        string `json:"state"`
					Action       string `json:"action"`
					LastCommit   struct {
						ID string `json:"id"`
					} `json:"last_commit"`
				}{
					IID:          42,
					Action:       "open",
					SourceBranch: "feature-branch",
					LastCommit: struct {
						ID string `json:"id"`
					}{ID: "abcdef1234567890"},
				},
				Project: struct {
					Name              string `json:"name"`
					PathWithNamespace string `json:"path_with_namespace"`
				}{
					PathWithNamespace: "org/repo",
				},
			},
			configData: map[string][]byte{
				"gitlab:org/repo:feature-branch": []byte(`
version: "1"
defaults:
  routing:
    header_key: x-custom-env
  lifecycle:
    ttl: 2h
services:
  web:
    image:
      repository: registry/web
      tag_template: ""
  api:
    image:
      repository: registry/api
`),
			},
			expectedStatus: http.StatusOK,
			verifyState: func(t *testing.T, c client.Client) {
				pg := &v1alpha1.PreviewGroup{}
				err := c.Get(context.Background(), client.ObjectKey{Name: "preview-mr-42"}, pg)
				if err != nil {
					t.Fatalf("expected PreviewGroup to be created, got err: %v", err)
				}
				if pg.Spec.Routing.HeaderKey != "x-custom-env" {
					t.Errorf("expected header_key x-custom-env, got %s", pg.Spec.Routing.HeaderKey)
				}
				if len(pg.Spec.Services) != 2 {
					t.Errorf("expected 2 services, got %d", len(pg.Spec.Services))
				}
			},
		},
		{
			name:        "MR Close Event",
			method:      http.MethodPost,
			token:       "secret",
			secretToken: "secret",
			payload: GitLabMRPayload{
				ObjectKind: "merge_request",
				ObjectAttributes: struct {
					ID           int    `json:"id"`
					IID          int    `json:"iid"`
					TargetBranch string `json:"target_branch"`
					SourceBranch string `json:"source_branch"`
					State        string `json:"state"`
					Action       string `json:"action"`
					LastCommit   struct {
						ID string `json:"id"`
					} `json:"last_commit"`
				}{
					IID:    42,
					Action: "close",
				},
			},
			expectedStatus: http.StatusOK,
			verifyState: func(t *testing.T, c client.Client) {
				pg := &v1alpha1.PreviewGroup{}
				err := c.Get(context.Background(), client.ObjectKey{Name: "preview-mr-42"}, pg)
				if err == nil {
					t.Fatalf("expected PreviewGroup to be deleted")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			// Pre-create for delete test
			if tt.payload.ObjectAttributes.Action == "close" {
				pg := &v1alpha1.PreviewGroup{
					ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42"},
				}
				_ = fakeClient.Create(context.Background(), pg)
			}

			handler := &GitLabPreviewGroupWebhookHandler{
				Client: fakeClient,
				Config: WebhookConfig{SecretToken: tt.secretToken},
				ConfigFetcher: &mockConfigFetcher{
					configData: tt.configData,
				},
			}

			body, _ := json.Marshal(tt.payload)
			req := httptest.NewRequest(tt.method, "/", bytes.NewReader(body))
			if tt.token != "" {
				req.Header.Set("X-Gitlab-Token", tt.token)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rec.Code)
			}

			if tt.verifyState != nil {
				tt.verifyState(t, fakeClient)
			}
		})
	}
}
