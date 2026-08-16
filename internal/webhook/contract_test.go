package webhook_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/webhook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func init() {
	_ = v1alpha1.AddToScheme(scheme.Scheme)
}

func TestContract_GitHubWebhook(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	handler := &webhook.GitHubWebhookHandler{
		Client:    fakeClient,
		Config:    webhook.WebhookConfig{SecretToken: "secret123"},
		DefaultNS: "default",
	}

	// 1. Missing signature -> 401
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// 2. Bad body (simulated by valid signature but bad json)
	payload := []byte(`{"action": "opened", "pull_request": {"number": 1, "head": {"ref": "main", "sha": "123"}, "base": {"ref": "main"}}, "repository": {"full_name": "owner/repo"}}`)
	badBody := []byte(`{bad`)
	badMac := hmac.New(sha256.New, []byte("secret123"))
	badMac.Write(badBody)
	badSig := "sha256=" + hex.EncodeToString(badMac.Sum(nil))

	req2 := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(badBody))
	req2.Header.Set("X-Hub-Signature-256", badSig)
	req2.Header.Set("X-GitHub-Event", "pull_request")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	assert.Equal(t, http.StatusBadRequest, rr2.Code)

	// 3. Valid -> 200 and K8s resource created
	mac := hmac.New(sha256.New, []byte("secret123"))
	mac.Write(payload)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req3 := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	req3.Header.Set("X-Hub-Signature-256", sig)
	req3.Header.Set("X-GitHub-Event", "pull_request")
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	assert.Equal(t, http.StatusOK, rr3.Code)

	// Verify K8s resource
	var envList v1alpha1.EnvironmentList
	err := fakeClient.List(context.Background(), &envList)
	require.NoError(t, err)
	require.Len(t, envList.Items, 1)

	env := envList.Items[0]
	assert.Equal(t, "github", env.Spec.Source.Provider)
	assert.Equal(t, "owner/repo", env.Spec.Source.Project)
	assert.Equal(t, 1, env.Spec.Source.MR)
}

func TestContract_GitLabWebhook(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	handler := &webhook.GitLabWebhookHandler{
		Client:    fakeClient,
		Config:    webhook.WebhookConfig{SecretToken: "secret123"},
		DefaultNS: "default",
	}

	// 1. Missing token -> 401
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// 2. Bad body -> 400
	req2 := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{bad`)))
	req2.Header.Set("X-Gitlab-Token", "secret123")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	assert.Equal(t, http.StatusBadRequest, rr2.Code)

	// 3. Valid -> 200 and K8s resource created
	payload := []byte(`{"object_kind": "merge_request", "object_attributes": {"iid": 42, "source_branch": "feature", "action": "open", "last_commit": {"id": "abcdef"}}, "project": {"path_with_namespace": "group/project"}}`)
	req3 := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	req3.Header.Set("X-Gitlab-Token", "secret123")
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	assert.Equal(t, http.StatusOK, rr3.Code)

	// Verify K8s resource
	var envList v1alpha1.EnvironmentList
	err := fakeClient.List(context.Background(), &envList)
	require.NoError(t, err)
	require.Len(t, envList.Items, 1)

	env := envList.Items[0]
	assert.Equal(t, "gitlab", env.Spec.Source.Provider)
	assert.Equal(t, "group/project", env.Spec.Source.Project)
	assert.Equal(t, 42, env.Spec.Source.MR)
}
