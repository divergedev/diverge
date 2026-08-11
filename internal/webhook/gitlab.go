package webhook

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/deployer"
	"github.com/divergedev/diverge/internal/metrics"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type WebhookConfig struct {
	SecretToken string
}

type GitLabWebhookHandler struct {
	Client        client.Client
	Config        WebhookConfig
	ConfigFetcher ConfigFetcher
	DefaultNS     string
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

	start := time.Now()
	action := "unknown"
	defer func() {
		metrics.WebhookProcessDuration.WithLabelValues("gitlab", action).Observe(time.Since(start).Seconds())
	}()

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

	action = normalizeGitLabAction(payload.ObjectAttributes.Action)

	logger.Info("Received GitLab MR event", "mr", payload.ObjectAttributes.IID, "action", payload.ObjectAttributes.Action)

	switch payload.ObjectAttributes.Action {
	case "open", "reopen", "update":
		if err := h.reconcileEnvironment(ctx, &payload); err != nil {
			logger.Error(err, "Failed to reconcile environment")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	case "merge", "close":
		if err := h.deleteEnvironment(ctx, &payload); err != nil {
			logger.Error(err, "Failed to delete environment")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (h *GitLabWebhookHandler) reconcileEnvironment(ctx context.Context, payload *GitLabMRPayload) error {
	envName := previewEnvName("mr", payload.Project.PathWithNamespace, payload.ObjectAttributes.IID)
	ns := h.DefaultNS
	if ns == "" {
		ns = "default"
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      envName,
			Namespace: ns,
		},
	}

	_, err := ctrl.CreateOrUpdate(ctx, h.Client, env, func() error {
		env.Spec.Source = v1alpha1.EnvironmentSource{
			Provider:  "gitlab",
			Project:   payload.Project.PathWithNamespace,
			MR:        payload.ObjectAttributes.IID,
			Branch:    payload.ObjectAttributes.SourceBranch,
			CommitSHA: payload.ObjectAttributes.LastCommit.ID,
		}

		if h.ConfigFetcher != nil {
			cfgData, err := h.ConfigFetcher.FetchConfig(ctx, "gitlab",
				payload.Project.PathWithNamespace,
				payload.ObjectAttributes.SourceBranch)
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					env.Spec.ServiceConfig = nil
					env.Spec.Deploy.ChangedServices = nil
					env.Spec.Routing.HeaderKey = ""
				} else {
					return err
				}
			} else {
				cfg, parseErr := deployer.ParseDotDivergeConfig(cfgData)
				if parseErr != nil {
					return parseErr
				}
				image := fmt.Sprintf("%s:%s",
					payload.Project.PathWithNamespace,
					payload.ObjectAttributes.LastCommit.ID[:12])
				env.Spec.ServiceConfig = cfg.ToServicePreviewConfig(image)
				env.Spec.Deploy.ChangedServices = []string{cfg.Spec.ServiceName}
				env.Spec.Deploy.Namespace = "same"
				env.Spec.Routing.HeaderKey = cfg.Spec.Routing.HeaderKey
			}
		} else {
			env.Spec.ServiceConfig = nil
			env.Spec.Deploy.ChangedServices = nil
			env.Spec.Routing.HeaderKey = ""
		}

		return nil
	})
	return err
}

func (h *GitLabWebhookHandler) deleteEnvironment(ctx context.Context, payload *GitLabMRPayload) error {
	envName := previewEnvName("mr", payload.Project.PathWithNamespace, payload.ObjectAttributes.IID)
	ns := h.DefaultNS
	if ns == "" {
		ns = "default"
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      envName,
			Namespace: ns,
		},
	}
	return client.IgnoreNotFound(h.Client.Delete(ctx, env))
}

// normalizeGitLabAction maps webhook actions to a bounded set of known values
// to prevent unbounded Prometheus label cardinality.
func normalizeGitLabAction(action string) string {
	switch action {
	case "open", "reopen", "update", "merge", "close", "approved", "unapproved":
		return action
	default:
		return "other"
	}
}
