package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/config"
	"github.com/divergedev/diverge/internal/metrics"
	"github.com/divergedev/diverge/internal/previewgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	traceparent := ResolveTraceparent(r)
	ctx := WithTraceparent(r.Context(), traceparent)
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

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
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

func (h *GitLabPreviewGroupWebhookHandler) resolveSingleMRPreviewGroupName(ctx context.Context, project string, iid int) (string, error) {
	defaultName := fmt.Sprintf("preview-mr-%d", iid)
	if strings.TrimSpace(project) != "" {
		qualName := previewEnvName("preview-mr", project, iid)
		if qualName != defaultName {
			var qualExisting v1alpha1.PreviewGroup
			if qErr := h.Client.Get(ctx, client.ObjectKey{Name: qualName}, &qualExisting); qErr == nil {
				return qualName, nil
			} else if !apierrors.IsNotFound(qErr) {
				return "", qErr
			}
		}
	}

	var existing v1alpha1.PreviewGroup
	err := h.Client.Get(ctx, client.ObjectKey{Name: defaultName}, &existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return defaultName, nil
		}
		return "", err
	}
	// If preview-mr-<iid> is unowned or belongs to the same GitLab project, reuse it;
	// otherwise disambiguate with the sanitized project slug so multi-project MRs never collide.
	if existing.Spec.Source.Project == "" || strings.EqualFold(existing.Spec.Source.Project, project) {
		return defaultName, nil
	}
	return previewEnvName("preview-mr", project, iid), nil
}

func (h *GitLabPreviewGroupWebhookHandler) reconcilePreviewGroup(ctx context.Context, payload *GitLabMRPayload) error {
	pgName, err := h.resolveSingleMRPreviewGroupName(ctx, payload.Project.PathWithNamespace, payload.ObjectAttributes.IID)
	if err != nil {
		return err
	}

	if h.ConfigFetcher == nil {
		return nil
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cfgData, err := h.ConfigFetcher.FetchConfig(fetchCtx, "gitlab",
		payload.Project.PathWithNamespace,
		payload.ObjectAttributes.SourceBranch)
	if err != nil {
		if errors.Is(err, config.ErrConfigNotFound) {
			// Do not create an invalid 0-service PreviewGroup when .diverge.yaml is absent; delete any prior single-MR group.
			return client.IgnoreNotFound(h.Client.Delete(ctx, &v1alpha1.PreviewGroup{ObjectMeta: metav1.ObjectMeta{Name: pgName}}))
		}
		return err
	}

	cfg, parseErr := config.Parse(cfgData)
	if parseErr != nil {
		return parseErr
	}
	if len(cfg.Services) == 0 {
		return client.IgnoreNotFound(h.Client.Delete(ctx, &v1alpha1.PreviewGroup{ObjectMeta: metav1.ObjectMeta{Name: pgName}}))
	}

	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: pgName,
		},
	}

	_, err = ctrl.CreateOrUpdate(ctx, h.Client, pg, func() error {
		SetTraceparentAnnotation(pg, TraceparentFromContext(ctx))
		pg.Spec.Source = v1alpha1.EnvironmentSource{
			Provider:  "gitlab",
			Project:   payload.Project.PathWithNamespace,
			MR:        payload.ObjectAttributes.IID,
			Branch:    payload.ObjectAttributes.SourceBranch,
			CommitSHA: payload.ObjectAttributes.LastCommit.ID,
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
			if d, dErr := time.ParseDuration(cfg.Defaults.Lifecycle.TTL); dErr == nil {
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
		return nil
	})
	if err != nil {
		return err
	}
	if syncErr := h.syncSharedGroupsFromSinglePG(ctx, pg, payload); syncErr != nil {
		log.FromContext(ctx).WithName("gitlab-previewgroup-webhook").Error(syncErr, "Best-effort shared group sync failed after saving single-MR PreviewGroup", "group", pg.Name)
	}
	return nil
}

func (h *GitLabPreviewGroupWebhookHandler) syncSharedGroupsFromSinglePG(ctx context.Context, singlePG *v1alpha1.PreviewGroup, payload *GitLabMRPayload) error {
	if singlePG == nil || len(singlePG.Spec.Services) == 0 {
		return nil
	}
	groups, err := previewgroup.FindGroupsForPROptional(ctx, h.Client, "gitlab", payload.Project.PathWithNamespace, payload.ObjectAttributes.IID, "")
	if err != nil || len(groups) == 0 {
		return err
	}
	nowStr := time.Now().UTC().Format(time.RFC3339)
	var svcs []v1alpha1.PreviewGroupServiceSpec
	var members []previewgroup.MemberRecord
	for _, svc := range singlePG.Spec.Services {
		if svc.Mode == v1alpha1.ServiceModeBaseline || svc.Image == "" {
			continue
		}
		svcs = append(svcs, *svc.DeepCopy())
		members = append(members, previewgroup.MemberRecord{
			Service:   svc.Name,
			Namespace: svc.Namespace,
			Provider:  "gitlab",
			Project:   payload.Project.PathWithNamespace,
			MR:        payload.ObjectAttributes.IID,
			Branch:    payload.ObjectAttributes.SourceBranch,
			UpdatedAt: nowStr,
		})
	}
	if len(svcs) == 0 {
		return nil
	}
	for _, sharedPG := range groups {
		if sharedPG.Name == singlePG.Name {
			continue
		}
		for i := range svcs {
			if _, jErr := previewgroup.Join(ctx, h.Client, previewgroup.JoinOptions{
				GroupName:               sharedPG.Name,
				Namespace:               sharedPG.Namespace,
				Services:                []v1alpha1.PreviewGroupServiceSpec{svcs[i]},
				Members:                 []previewgroup.MemberRecord{members[i]},
				SyncOnlyIfMember:        true,
				PreserveServiceDatabase: true,
				Traceparent:             TraceparentFromContext(ctx),
			}); jErr != nil {
				return jErr
			}
		}
	}
	return nil
}

func (h *GitLabPreviewGroupWebhookHandler) deletePreviewGroup(ctx context.Context, payload *GitLabMRPayload) error {
	defaultName := fmt.Sprintf("preview-mr-%d", payload.ObjectAttributes.IID)
	var existing v1alpha1.PreviewGroup
	if err := h.Client.Get(ctx, client.ObjectKey{Name: defaultName}, &existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
	} else {
		if existing.Spec.Source.Project == "" || payload.Project.PathWithNamespace == "" ||
			strings.EqualFold(existing.Spec.Source.Project, payload.Project.PathWithNamespace) {
			if delErr := client.IgnoreNotFound(h.Client.Delete(ctx, &existing)); delErr != nil {
				return delErr
			}
		}
	}
	if payload.Project.PathWithNamespace != "" {
		qualName := previewEnvName("preview-mr", payload.Project.PathWithNamespace, payload.ObjectAttributes.IID)
		if qualName != defaultName {
			if delErr := client.IgnoreNotFound(h.Client.Delete(ctx, &v1alpha1.PreviewGroup{ObjectMeta: metav1.ObjectMeta{Name: qualName}})); delErr != nil {
				return delErr
			}
		}
	}
	_, err := previewgroup.DetachMemberByPR(ctx, h.Client, "gitlab", payload.Project.PathWithNamespace, payload.ObjectAttributes.IID, "")
	return err
}
