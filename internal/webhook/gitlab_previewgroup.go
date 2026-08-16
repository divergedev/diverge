package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/config"
	"github.com/divergedev/diverge/internal/metrics"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type GitLabPreviewGroupWebhookHandler struct {
	Client        client.Client
	Config        WebhookConfig
	ConfigFetcher ConfigFetcher
}

func (h *GitLabPreviewGroupWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx).WithName("gitlab-previewgroup-webhook")

	start := time.Now()
	action := "unknown"
	defer func() {
		metrics.WebhookProcessDuration.WithLabelValues("gitlab", action).Observe(time.Since(start).Seconds())
	}()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !ValidateGitLabWebhookToken(r, h.Config.SecretToken) {
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
		if err := h.reconcilePreviewGroup(ctx, &payload); err != nil {
			logger.Error(err, "Failed to reconcile PreviewGroup")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	case "merge", "close":
		if err := h.deletePreviewGroup(ctx, &payload); err != nil {
			logger.Error(err, "Failed to delete PreviewGroup")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (h *GitLabPreviewGroupWebhookHandler) reconcilePreviewGroup(ctx context.Context, payload *GitLabMRPayload) error {
	pgName := fmt.Sprintf("preview-mr-%d", payload.ObjectAttributes.IID)

	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: pgName,
		},
	}

	_, err := ctrl.CreateOrUpdate(ctx, h.Client, pg, func() error {
		pg.Spec.Source = v1alpha1.EnvironmentSource{
			Provider:  "gitlab",
			Project:   payload.Project.PathWithNamespace,
			MR:        payload.ObjectAttributes.IID,
			Branch:    payload.ObjectAttributes.SourceBranch,
			CommitSHA: payload.ObjectAttributes.LastCommit.ID,
		}

		if h.ConfigFetcher != nil {
			fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			cfgData, err := h.ConfigFetcher.FetchConfig(fetchCtx, "gitlab",
				payload.Project.PathWithNamespace,
				payload.ObjectAttributes.SourceBranch)
			if err != nil {
				if errors.Is(err, config.ErrConfigNotFound) {
					// No config found, clear services
					pg.Spec.Services = nil
					pg.Spec.Routing = v1alpha1.PreviewGroupRouting{}
				} else {
					return err
				}
			} else {
				cfg, parseErr := config.Parse(cfgData)
				if parseErr != nil {
					return parseErr
				}

				pg.Spec.Routing = v1alpha1.PreviewGroupRouting{
					Mode:        "header",
					HeaderKey:   cfg.Defaults.Routing.HeaderKey,
					HeaderValue: fmt.Sprintf("%d", payload.ObjectAttributes.IID),
				}
				if pg.Spec.Routing.HeaderKey == "" {
					pg.Spec.Routing.HeaderKey = "x-preview-env" // default
				}
				if cfg.Defaults.Routing.Mode != "" {
					pg.Spec.Routing.Mode = cfg.Defaults.Routing.Mode
				}

				if cfg.Defaults.Lifecycle.TTL != "" {
					if d, err := time.ParseDuration(cfg.Defaults.Lifecycle.TTL); err == nil {
						pg.Spec.Lifecycle = &v1alpha1.PreviewGroupLifecycle{
							TTL: &metav1.Duration{Duration: d},
						}
					}
				}

				if cfg.Defaults.Lifecycle.CleanupOnMerge != nil {
					if pg.Spec.Lifecycle == nil {
						pg.Spec.Lifecycle = &v1alpha1.PreviewGroupLifecycle{}
					}
					pg.Spec.Lifecycle.CleanupOnMerge = *cfg.Defaults.Lifecycle.CleanupOnMerge
				}

				imageTag := payload.ObjectAttributes.LastCommit.ID
				if len(imageTag) > 12 {
					imageTag = imageTag[:12]
				}
				image := fmt.Sprintf("registry.gitlab.com/%s:%s", payload.Project.PathWithNamespace, imageTag)

				var svcs []v1alpha1.PreviewGroupServiceSpec
				var keys []string
				for k := range cfg.Services {
					keys = append(keys, k)
				}
				sort.Strings(keys)

				for _, name := range keys {
					svcCfg := cfg.Services[name]
					s := v1alpha1.PreviewGroupServiceSpec{
						Name:  name,
						Image: image, // fallback used when Repository is empty
					}
					if svcCfg.Image.Repository != "" {
						tag := svcCfg.Image.TagTemplate
						if tag == "" {
							tag = imageTag
						}
						s.Image = fmt.Sprintf("%s:%s", svcCfg.Image.Repository, tag)
					}
					svcs = append(svcs, s)
				}
				pg.Spec.Services = svcs
			}
		} else {
			pg.Spec.Services = nil
			pg.Spec.Routing = v1alpha1.PreviewGroupRouting{}
		}

		return nil
	})
	return err
}

func (h *GitLabPreviewGroupWebhookHandler) deletePreviewGroup(ctx context.Context, payload *GitLabMRPayload) error {
	pgName := fmt.Sprintf("preview-mr-%d", payload.ObjectAttributes.IID)

	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: pgName,
		},
	}
	return client.IgnoreNotFound(h.Client.Delete(ctx, pg))
}
