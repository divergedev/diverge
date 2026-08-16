package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGitLabPreviewGroupNotifier_PostGroupCreated(t *testing.T) {
	var requestedMethod string
	var requestedURL string
	var requestBody map[string]string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedMethod = r.Method
		requestedURL = r.URL.String()

		if r.Header.Get("PRIVATE-TOKEN") != "test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		_ = json.NewDecoder(r.Body).Decode(&requestBody)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": 12345,
		})
	}))
	defer ts.Close()

	n := NewGitLabPreviewGroupNotifier(ts.URL, "test-token")

	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-1"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}

	err := n.PostGroupCreated(context.Background(), pg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if requestedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", requestedMethod)
	}

	expectedURL := "/api/v4/projects/owner%2Frepo/merge_requests/42/notes"
	if requestedURL != expectedURL {
		t.Errorf("expected URL %s, got %s", expectedURL, requestedURL)
	}

	if pg.Status.CommentID != 12345 {
		t.Errorf("expected CommentID to be set to 12345, got %d", pg.Status.CommentID)
	}
}

func TestGitLabPreviewGroupNotifier_PostGroupReady_Update(t *testing.T) {
	var requestedMethod string
	var requestedURL string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedMethod = r.Method
		requestedURL = r.URL.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	n := NewGitLabPreviewGroupNotifier(ts.URL, "test-token")

	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-1"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "owner/repo",
				MR:       42,
			},
		},
		Status: v1alpha1.PreviewGroupStatus{
			CommentID: 12345,
		},
	}

	err := n.PostGroupReady(context.Background(), pg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if requestedMethod != http.MethodPut {
		t.Errorf("expected PUT, got %s", requestedMethod)
	}

	expectedURL := "/api/v4/projects/owner%2Frepo/merge_requests/42/notes/12345"
	if requestedURL != expectedURL {
		t.Errorf("expected URL %s, got %s", expectedURL, requestedURL)
	}
}

func TestGitLabPreviewGroupNotifier_Errors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	n := NewGitLabPreviewGroupNotifier(ts.URL, "test-token")

	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-1"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}

	err := n.PostGroupCreated(context.Background(), pg)
	if err == nil {
		t.Fatal("expected error, got none")
	}

	// Missing MR
	pg.Spec.Source.MR = 0
	err = n.PostGroupCreated(context.Background(), pg)
	if err == nil {
		t.Fatal("expected error for missing MR, got none")
	}

	// Wrong provider
	pg.Spec.Source.MR = 42
	pg.Spec.Source.Provider = "github"
	err = n.PostGroupCreated(context.Background(), pg)
	if err == nil {
		t.Fatal("expected error for wrong provider, got none")
	}
}

func TestGitLabPreviewGroupNotifier_Coverage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	n := NewGitLabPreviewGroupNotifier(ts.URL, "test-token")

	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-1"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "owner/repo",
				MR:       42,
			},
		},
		Status: v1alpha1.PreviewGroupStatus{
			CommentID: 12345,
		},
	}

	if err := n.PostGroupFailed(context.Background(), pg, "test failure"); err != nil {
		t.Errorf("PostGroupFailed: %v", err)
	}
	if err := n.PostGroupTeardown(context.Background(), pg); err != nil {
		t.Errorf("PostGroupTeardown: %v", err)
	}
	if err := n.UpdateGroupStatus(context.Background(), pg); err != nil {
		t.Errorf("UpdateGroupStatus: %v", err)
	}
}
