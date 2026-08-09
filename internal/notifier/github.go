package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type GitHubNotifier struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func NewGitHubNotifier(baseURL, token string) *GitHubNotifier {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &GitHubNotifier{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (g *GitHubNotifier) getProjectAndPR(env *v1alpha1.Environment) (string, int, error) {
	if env.Spec.Source.Provider != "github" {
		return "", 0, fmt.Errorf("environment is not from github")
	}
	return env.Spec.Source.Project, env.Spec.Source.MR, nil
}

func (g *GitHubNotifier) getCommentID(env *v1alpha1.Environment) int {
	if env.Annotations == nil {
		return 0
	}
	idStr := env.Annotations["diverge.io/github-pr-comment-id"]
	if idStr == "" {
		return 0
	}
	id, _ := strconv.Atoi(idStr)
	return id
}

func (g *GitHubNotifier) setCommentID(env *v1alpha1.Environment, id int) {
	if env.Annotations == nil {
		env.Annotations = make(map[string]string)
	}
	env.Annotations["diverge.io/github-pr-comment-id"] = strconv.Itoa(id)
}

func (g *GitHubNotifier) postOrUpdateComment(ctx context.Context, env *v1alpha1.Environment, body string) error {
	project, pr, err := g.getProjectAndPR(env) // project format: owner/repo
	if err != nil {
		return err
	}

	commentID := g.getCommentID(env)

	payload := map[string]string{"body": body}
	jsonPayload, _ := json.Marshal(payload)

	var reqURL string
	var method string

	if commentID != 0 {
		reqURL = fmt.Sprintf("%s/repos/%s/issues/comments/%d", g.BaseURL, project, commentID)
		method = http.MethodPatch
	} else {
		reqURL = fmt.Sprintf("%s/repos/%s/issues/%d/comments", g.BaseURL, project, pr)
		method = http.MethodPost
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bytes.NewReader(jsonPayload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := g.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github API returned status: %d", resp.StatusCode)
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

func (g *GitHubNotifier) PostEnvironmentCreated(ctx context.Context, env *v1alpha1.Environment) error {
	data := buildTemplateData(env, "")
	msg, err := renderTemplate(createdTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, env, msg)
}

func (g *GitHubNotifier) PostEnvironmentReady(ctx context.Context, env *v1alpha1.Environment) error {
	data := buildTemplateData(env, "")
	msg, err := renderTemplate(readyTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, env, msg)
}

func (g *GitHubNotifier) PostEnvironmentFailed(ctx context.Context, env *v1alpha1.Environment, reason string) error {
	data := buildTemplateData(env, reason)
	msg, err := renderTemplate(failedTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, env, msg)
}

func (g *GitHubNotifier) PostEnvironmentTeardown(ctx context.Context, env *v1alpha1.Environment) error {
	data := buildTemplateData(env, "Teardown requested")
	msg, err := renderTemplate(teardownTemplate, data)
	if err != nil {
		return err
	}
	return g.postOrUpdateComment(ctx, env, msg)
}

func (g *GitHubNotifier) UpdateEnvironmentStatus(ctx context.Context, env *v1alpha1.Environment) error {
	return nil
}
