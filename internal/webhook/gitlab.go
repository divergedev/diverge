package webhook

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type WebhookConfig struct {
	SecretToken string
}

type GitLabWebhookHandler struct {
	Client client.Client
	Config WebhookConfig
}

// GitLabMRPayload is a simple struct to decode MR webhook payloads
type GitLabMRPayload struct {
	ObjectKind       string `json:"object_kind"`
	EventType        string `json:"event_type"`
	ObjectAttributes struct {
		ID           int    `json:"id"`
		IID          int    `json:"iid"`
		TargetBranch string `json:"target_branch"`
		SourceBranch string `json:"source_branch"`
		State        string `json:"state"`
		Action       string `json:"action"`
		LastCommit   struct {
			ID string `json:"id"`
		} `json:"last_commit"`
	} `json:"object_attributes"`
	Project struct {
		Name              string `json:"name"`
		PathWithNamespace string `json:"path_with_namespace"`
	} `json:"project"`
}

func (h *GitLabWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx).WithName("gitlab-webhook")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := r.Header.Get("X-Gitlab-Token")
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(h.Config.SecretToken)) != 1 {
		logger.Info("Unauthorized webhook request")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var payload GitLabMRPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		logger.Error(err, "Failed to decode payload")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if payload.ObjectKind != "merge_request" {
		w.WriteHeader(http.StatusOK)
		return
	}

	logger.Info("Received GitLab MR event", "mr", payload.ObjectAttributes.IID, "action", payload.ObjectAttributes.Action)

	// Create/Update/Delete Environment CRs based on action (open, update, merge, close)
	// Read labels from MR to determine deploy mode and DB strategy

	w.WriteHeader(http.StatusOK)
}
