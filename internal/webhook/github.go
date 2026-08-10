package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type GitHubWebhookHandler struct {
	Client client.Client
	Config WebhookConfig
}

type GitHubPRPayload struct {
	Action      string `json:"action"`
	PullRequest struct {
		Number int `json:"number"`
		Head   struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
		State string `json:"state"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func (h *GitHubWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx).WithName("github-webhook")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	signature := r.Header.Get("X-Hub-Signature-256")
	if signature == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	expectedMAC := hmac.New(sha256.New, []byte(h.Config.SecretToken))
	expectedMAC.Write(bodyBytes)
	expectedSignature := "sha256=" + hex.EncodeToString(expectedMAC.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
		logger.Info("Unauthorized webhook request")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	if event != "pull_request" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var payload GitHubPRPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		logger.Error(err, "Failed to decode payload")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	logger.Info("Received GitHub PR event", "pr", payload.PullRequest.Number, "action", payload.Action)

	// Same Environment CR creation logic as GitLab handler

	w.WriteHeader(http.StatusOK)
}
