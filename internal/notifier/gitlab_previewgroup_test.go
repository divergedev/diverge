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
		if r.Method == http.MethodGet && r.URL.Path == "/api/v4/user" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 99, "username": "diverge-bot"}`))
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}
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

func TestGitLabPreviewGroupNotifier_RecoversExistingNoteByMarker(t *testing.T) {
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-recover"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}
	marker := previewGroupCommentMarker(pg)
	var methods []string
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v4/user" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 99, "username": "diverge-bot"}`))
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": 9999, "body": "human quoted\n\n" + marker, "author": map[string]interface{}{"id": 500, "username": "alice"}},
				{"id": 9001, "body": "prior note\n\n" + marker, "author": map[string]interface{}{"id": 99, "username": "diverge-bot"}},
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 9001}`))
	}))
	defer ts.Close()

	n := NewGitLabPreviewGroupNotifier(ts.URL, "test-token")
	if err := n.PostGroupReady(context.Background(), pg); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(methods) != 3 || methods[0] != http.MethodGet || methods[1] != http.MethodGet || methods[2] != http.MethodPut {
		t.Fatalf("expected [GET GET PUT], got %v", methods)
	}
	if paths[2] != "/api/v4/projects/owner%2Frepo/merge_requests/42/notes/9001" {
		t.Fatalf("unexpected note update path: %s", paths[2])
	}
	if pg.Status.CommentID != 9001 {
		t.Fatalf("expected recovered CommentID 9001, got %d", pg.Status.CommentID)
	}
}

func TestGitLabPreviewGroupNotifier_IgnoresHumanQuotedMarkerAndReusesLearnedAuthor(t *testing.T) {
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-learned-author"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}
	marker := previewGroupCommentMarker(pg)
	allowUserCall := true
	notesList := []map[string]interface{}{
		{"id": 9999, "body": "human quoted\n\n" + marker, "author": map[string]interface{}{"id": 500, "username": "alice"}},
	}
	var methods []string
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v4/user" {
			if !allowUserCall {
				t.Fatalf("unexpected /api/v4/user request after author identity was learned")
			}
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(notesList)
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 9001, "author": {"id": 99, "username": "diverge-bot"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 9001}`))
	}))
	defer ts.Close()

	n := NewGitLabPreviewGroupNotifier(ts.URL, "test-token")
	if err := n.PostGroupCreated(context.Background(), pg); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(methods) != 2 || methods[0] != http.MethodGet || methods[1] != http.MethodPost {
		t.Fatalf("expected [GET POST], got %v", methods)
	}
	if pg.Status.CommentID != 9001 {
		t.Fatalf("expected CommentID 9001, got %d", pg.Status.CommentID)
	}

	allowUserCall = false
	pg.Status.CommentID = 0
	notesList = append([]map[string]interface{}{
		{"id": 9001, "body": "created\n\n" + marker, "author": map[string]interface{}{"id": 99, "username": "diverge-bot"}},
	}, notesList...)
	methods = nil
	paths = nil

	if err := n.PostGroupReady(context.Background(), pg); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(methods) != 2 || methods[0] != http.MethodGet || methods[1] != http.MethodPut {
		t.Fatalf("expected [GET PUT], got %v (%v)", methods, paths)
	}
	if pg.Status.CommentID != 9001 {
		t.Fatalf("expected recovered CommentID 9001, got %d", pg.Status.CommentID)
	}
}

func TestGitLabPreviewGroupNotifier_RecoveredIDStaysProvisionalOnPutFailure(t *testing.T) {
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-provisional"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}
	marker := previewGroupCommentMarker(pg)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v4/user" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 99, "username": "diverge-bot"}`))
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": 9001, "body": marker, "author": map[string]interface{}{"id": 99, "username": "diverge-bot"}},
			})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	n := NewGitLabPreviewGroupNotifier(ts.URL, "test-token")
	if err := n.PostGroupReady(context.Background(), pg); err == nil {
		t.Fatalf("expected PUT error, got nil")
	}
	if pg.Status.CommentID != 0 {
		t.Fatalf("recovered note ID must stay provisional until PUT succeeds, got %d", pg.Status.CommentID)
	}
}

func TestGitLabPreviewGroupNotifier_TemporaryUserLookupFailureRetainsExistingNote(t *testing.T) {
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-temp-user-err"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}
	marker := previewGroupCommentMarker(pg)
	userStep := 0
	var methods []string
	var postCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v4/user" {
			userStep++
			switch userStep {
			case 1:
				w.WriteHeader(http.StatusInternalServerError)
			case 2:
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id": 99, "username":`))
			default:
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id": 99, "username": "diverge-bot"}`))
			}
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": 9001, "body": "existing\n\n" + marker, "author": map[string]interface{}{"id": 99, "username": "diverge-bot"}},
			})
			return
		}
		if r.Method == http.MethodPost {
			postCount++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 9002}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 9001}`))
	}))
	defer ts.Close()

	n := NewGitLabPreviewGroupNotifier(ts.URL, "test-token")
	if err := n.PostGroupReady(context.Background(), pg); err == nil {
		t.Fatalf("expected error on temporary /api/v4/user 500, got nil")
	}
	if postCount != 0 || pg.Status.CommentID != 0 {
		t.Fatalf("expected no duplicate POST and zero CommentID, got postCount=%d CommentID=%d", postCount, pg.Status.CommentID)
	}

	if err := n.PostGroupReady(context.Background(), pg); err == nil {
		t.Fatalf("expected error on truncated /api/v4/user JSON, got nil")
	}
	if postCount != 0 || pg.Status.CommentID != 0 {
		t.Fatalf("expected no duplicate POST and zero CommentID on truncated JSON, got postCount=%d CommentID=%d", postCount, pg.Status.CommentID)
	}

	if err := n.PostGroupReady(context.Background(), pg); err != nil {
		t.Fatalf("expected recovery after /api/v4/user recovers, got %v", err)
	}
	if postCount != 0 {
		t.Fatalf("expected no duplicate POST after /api/v4/user recovery, got %d", postCount)
	}
	if len(methods) != 5 || methods[0] != http.MethodGet || methods[1] != http.MethodGet || methods[2] != http.MethodGet || methods[3] != http.MethodGet || methods[4] != http.MethodPut {
		t.Fatalf("expected [GET GET GET GET PUT], got %v", methods)
	}
	if pg.Status.CommentID != 9001 {
		t.Fatalf("expected recovered CommentID 9001, got %d", pg.Status.CommentID)
	}
}
