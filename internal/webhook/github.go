package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

type GitHubWebhookHandler struct {
	Client        client.Client
	Config        WebhookConfig
	ConfigFetcher ConfigFetcher
	DefaultNS     string
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

	start := time.Now()
	action := "unknown"
	defer func() {
		metrics.WebhookProcessDuration.WithLabelValues("github", action).Observe(time.Since(start).Seconds())
	}()

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

	action = normalizeGitHubAction(payload.Action)

	logger.Info("Received GitHub PR event", "pr", payload.PullRequest.Number, "action", payload.Action)

	switch payload.Action {
	case "opened", "reopened", "synchronize":
		if err := h.reconcileEnvironment(ctx, &payload); err != nil {
			logger.Error(err, "Failed to reconcile environment")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	case "closed":
		if err := h.deleteEnvironment(ctx, &payload); err != nil {
			logger.Error(err, "Failed to delete environment")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (h *GitHubWebhookHandler) reconcileEnvironment(ctx context.Context, payload *GitHubPRPayload) error {
	envName := previewEnvName("pr", payload.Repository.FullName, payload.PullRequest.Number)
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
			Provider:  "github",
			Project:   payload.Repository.FullName,
			MR:        payload.PullRequest.Number,
			Branch:    payload.PullRequest.Head.Ref,
			CommitSHA: payload.PullRequest.Head.SHA,
		}

		if h.ConfigFetcher != nil {
			cfgData, err := h.ConfigFetcher.FetchConfig(ctx, "github",
				payload.Repository.FullName,
				payload.PullRequest.Head.Ref)
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
					payload.Repository.FullName,
					safeSHA(payload.PullRequest.Head.SHA, 12))
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

func (h *GitHubWebhookHandler) deleteEnvironment(ctx context.Context, payload *GitHubPRPayload) error {
	envName := previewEnvName("pr", payload.Repository.FullName, payload.PullRequest.Number)
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

// normalizeGitHubAction maps webhook actions to a bounded set of known values
// to prevent unbounded Prometheus label cardinality.
func normalizeGitHubAction(action string) string {
	switch action {
	case "opened", "synchronize", "closed", "reopened", "edited", "ready_for_review":
		return action
	default:
		return "other"
	}
}
