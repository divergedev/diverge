package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type GitLabNotifier struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func NewGitLabNotifier(baseURL, token string) *GitLabNotifier {
	return &GitLabNotifier{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (g *GitLabNotifier) getProjectAndMR(env *v1alpha1.Environment) (string, int, error) {
	if env.Spec.Source.Provider != "gitlab" {
		return "", 0, fmt.Errorf("environment is not from gitlab")
	}
	return url.PathEscape(env.Spec.Source.Project), env.Spec.Source.MR, nil
}

func (g *GitLabNotifier) getCommentID(env *v1alpha1.Environment) int {
	if env.Annotations == nil {
		return 0
	}
	idStr := env.Annotations["diverge.io/gitlab-mr-comment-id"]
	if idStr == "" {
		return 0
	}
	id, _ := strconv.Atoi(idStr)
	return id
}

func (g *GitLabNotifier) setCommentID(env *v1alpha1.Environment, id int) {
	if env.Annotations == nil {
		env.Annotations = make(map[string]string)
	}
	env.Annotations["diverge.io/gitlab-mr-comment-id"] = strconv.Itoa(id)
}

func (g *GitLabNotifier) postOrUpdateComment(ctx context.Context, env *v1alpha1.Environment, body string) error {
	project, mr, err := g.getProjectAndMR(env)
	if err != nil {
		return err
	}

	commentID := g.getCommentID(env)

	payload := map[string]string{"body": body}
	jsonPayload, _ := json.Marshal(payload)

	var reqURL string
	var method string

	if commentID != 0 {
		reqURL = fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d/notes/%d", g.BaseURL, project, mr, commentID)
		method = http.MethodPut
	} else {
		reqURL = fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d/notes", g.BaseURL, project, mr)
		method = http.MethodPost
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bytes.NewReader(jsonPayload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PRIVATE-TOKEN", g.Token)

	resp, err := g.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gitlab API returned status: %d", resp.StatusCode)
	}

	if commentID == 0 {
		var result struct {
			ID int `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.ID != 0 {
			g.setCommentID(env, result.ID)
		}
	}

	return nil
}

func buildTemplateData(env *v1alpha1.Environment, reason string) TemplateData {
	ttlStr := "never"
	if env.Spec.Lifecycle.TTL != nil {
		ttlStr = env.Spec.Lifecycle.TTL.Duration.String()
	}

	durationStr := "-"
	if env.Status.CreatedAt != nil {
		durationStr = time.Since(env.Status.CreatedAt.Time).String()
	}

	expiryStr := "never"
	if env.Status.ExpiresAt != nil {
		expiryStr = env.Status.ExpiresAt.Format(time.RFC3339)
	}

	var conditions []ConditionData
	for _, c := range env.Status.Conditions {
		icon := "✅"
		if c.Status == "False" {
			icon = "❌"
		}
		conditions = append(conditions, ConditionData{
			Icon:    icon,
			Type:    c.Type,
			Message: c.Message,
		})
	}

	baseURL := ""
	if env.Status.URL != "" {
		baseURL = env.Status.URL
	} else if env.Spec.Routing.Mode == "subdomain" {
		baseURL = fmt.Sprintf("https://%s.preview.example.com", env.Name) // domain not fully known here, assume logic
	}

	return TemplateData{
		Name:        env.Name,
		Branch:      env.Spec.Source.Branch,
		Mode:        env.Spec.Deploy.Mode,
		RoutingMode: env.Spec.Routing.Mode,
		Services:    env.Spec.Deploy.ChangedServices,
		TTL:         ttlStr,
		URL:         env.Status.URL,
		NumServices: len(env.Spec.Deploy.ChangedServices),
		Duration:    durationStr,
		BaseURL:     baseURL,
		ExpiryTime:  expiryStr,
		Reason:      reason,
		Conditions:  conditions,
	}
}

func (g *GitLabNotifier) PostEnvironmentCreated(ctx context.Context, env *v1alpha1.Environment) error {
	data := buildTemplateData(env, "")
	msg, err := renderTemplate(createdTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, env, msg)
}

func (g *GitLabNotifier) PostEnvironmentReady(ctx context.Context, env *v1alpha1.Environment) error {
	data := buildTemplateData(env, "")
	msg, err := renderTemplate(readyTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, env, msg)
}

func (g *GitLabNotifier) PostEnvironmentFailed(ctx context.Context, env *v1alpha1.Environment, reason string) error {
	data := buildTemplateData(env, reason)
	msg, err := renderTemplate(failedTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, env, msg)
}

func (g *GitLabNotifier) PostEnvironmentTeardown(ctx context.Context, env *v1alpha1.Environment) error {
	data := buildTemplateData(env, "Teardown requested")
	msg, err := renderTemplate(teardownTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, env, msg)
}

func (g *GitLabNotifier) UpdateEnvironmentStatus(ctx context.Context, env *v1alpha1.Environment) error {
	data := buildTemplateData(env, "")
	msg, err := renderTemplate(readyTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, env, msg)
}
