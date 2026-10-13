package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
)

const (
	// AgentTaskCommentMarker is the hidden HTML marker used to identify AgentTask PR/MR comments.
	AgentTaskCommentMarker = "<!-- diverge-agent-task -->"

	annotationPRNumber           = "divergedev.com/pr-number"
	annotationMRNumber           = "divergedev.com/mr-number"
	annotationMRIID              = "divergedev.com/mr-iid"
	annotationProject            = "divergedev.com/project"
	annotationPreviewURL         = "divergedev.com/preview-url"
	annotationPreviewEnvironment = "divergedev.com/preview-environment"
	annotationPreviewBranch      = "divergedev.com/preview-branch"
)

var (
	prBranchRegex = regexp.MustCompile(`(?i)(?:^|/|-)pr-(\d+)(?:$|-|/)`)
	prURLRegex    = regexp.MustCompile(`/(?:pull|merge_requests)/(\d+)(?:$|[/?#])`)
	scpRepoRegex  = regexp.MustCompile(`^[^@]+@[^:]+:(.+)$`)
)

// TaskPhaseNotifier notifies external observers when an AgentTask changes phases.
type TaskPhaseNotifier interface {
	NotifyPhaseChange(ctx context.Context, task *v1alpha1.AgentTask, oldPhase, newPhase v1alpha1.AgentTaskPhase) error
}

// NoopTaskPhaseNotifier is a default no-op implementation of TaskPhaseNotifier.
type NoopTaskPhaseNotifier struct{}

// NotifyPhaseChange does nothing.
func (n *NoopTaskPhaseNotifier) NotifyPhaseChange(_ context.Context, _ *v1alpha1.AgentTask, _, _ v1alpha1.AgentTaskPhase) error {
	return nil
}

// TaskProviders is the registry of TaskPhaseNotifier providers.
var TaskProviders = registry.New[TaskPhaseNotifier]("task_notifier")

func init() {
	TaskProviders.Register("noop", registry.Provider[TaskPhaseNotifier]{
		Create: func(_ registry.Deps) (TaskPhaseNotifier, error) {
			return &NoopTaskPhaseNotifier{}, nil
		},
		Description: "No-op AgentTask phase notifications",
	})

	TaskProviders.Register("github", registry.Provider[TaskPhaseNotifier]{
		Create: func(deps registry.Deps) (TaskPhaseNotifier, error) {
			token := firstNonEmptyEnv("DIVERGE_NOTIFIER_TOKEN", "GITHUB_TOKEN")
			baseURL := firstNonEmptyEnv("DIVERGE_BASE_URL", "GITHUB_API_URL")
			if tn := NewTaskNotifier("github", token, baseURL, deps.Client); tn != nil {
				return tn, nil
			}
			return &NoopTaskPhaseNotifier{}, nil
		},
		Description: "GitHub PR AgentTask phase notifications",
	})

	TaskProviders.Register("gitlab", registry.Provider[TaskPhaseNotifier]{
		Create: func(deps registry.Deps) (TaskPhaseNotifier, error) {
			token := firstNonEmptyEnv("DIVERGE_NOTIFIER_TOKEN", "DIVERGE_GITLAB_TOKEN", "GITLAB_TOKEN")
			baseURL := firstNonEmptyEnv("DIVERGE_BASE_URL", "GITLAB_API_URL")
			if tn := NewTaskNotifier("gitlab", token, baseURL, deps.Client); tn != nil {
				return tn, nil
			}
			return &NoopTaskPhaseNotifier{}, nil
		},
		Description: "GitLab MR AgentTask phase notifications",
	})
}

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// SCMCommentClient abstracts PR/MR comment lookup, creation, and updates across GitHub and GitLab.
type SCMCommentClient interface {
	FindExistingComment(ctx context.Context, project string, mrID int, marker string) (int64, error)
	PostComment(ctx context.Context, project string, mrID int, body string) (int64, error)
	UpdateComment(ctx context.Context, project string, mrID int, commentID int64, body string) error
}

// SCMTaskNotifier posts and updates autonomous AgentTask progress comments on GitHub PRs and GitLab MRs.
type SCMTaskNotifier struct {
	Underlying SCMCommentClient
	Client     client.Client
}

// NewSCMTaskNotifier creates an SCMTaskNotifier backed by the provided SCMCommentClient.
func NewSCMTaskNotifier(underlying SCMCommentClient) *SCMTaskNotifier {
	return &SCMTaskNotifier{Underlying: underlying}
}

// NewGitHubTaskNotifier creates an SCMTaskNotifier configured for GitHub PR comments.
func NewGitHubTaskNotifier(token, baseURL string, k8sClient client.Client) *SCMTaskNotifier {
	cleanBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	gh := NewGitHubNotifier(cleanBase, strings.TrimSpace(token))
	return &SCMTaskNotifier{
		Underlying: gh,
		Client:     k8sClient,
	}
}

// NewGitLabTaskNotifier creates an SCMTaskNotifier configured for GitLab MR notes.
func NewGitLabTaskNotifier(token, baseURL string, k8sClient client.Client) *SCMTaskNotifier {
	cleanBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if cleanBase == "" {
		cleanBase = "https://gitlab.com"
	}
	gl := NewGitLabNotifier(cleanBase, strings.TrimSpace(token))
	return &SCMTaskNotifier{
		Underlying: gl,
		Client:     k8sClient,
	}
}

// NewTaskNotifier creates a provider-specific SCMTaskNotifier when provider is "github" or "gitlab"
// and token is non-empty; otherwise returns nil.
func NewTaskNotifier(provider, token, baseURL string, k8sClient client.Client) *SCMTaskNotifier {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github":
		return NewGitHubTaskNotifier(token, baseURL, k8sClient)
	case "gitlab":
		return NewGitLabTaskNotifier(token, baseURL, k8sClient)
	default:
		return nil
	}
}

// NotifyTaskStarted posts or updates the PR/MR comment when an AgentTask starts running.
func (n *SCMTaskNotifier) NotifyTaskStarted(ctx context.Context, task *v1alpha1.AgentTask) error {
	return n.upsertTaskComment(ctx, task, "🚀 Running", "")
}

// NotifyTaskCompleted posts or updates the PR/MR comment when an AgentTask completes.
func (n *SCMTaskNotifier) NotifyTaskCompleted(ctx context.Context, task *v1alpha1.AgentTask) error {
	return n.upsertTaskComment(ctx, task, "✅ Completed", "")
}

// NotifyTaskFailed posts or updates the PR/MR comment when an AgentTask fails.
func (n *SCMTaskNotifier) NotifyTaskFailed(ctx context.Context, task *v1alpha1.AgentTask, reason string) error {
	if reason == "" && task != nil {
		reason = task.Status.Message
	}
	return n.upsertTaskComment(ctx, task, "❌ Failed", reason)
}

// NotifyPhaseChange implements controller.TaskNotifier to bridge phase transitions to SCM PR/MR comments.
func (n *SCMTaskNotifier) NotifyPhaseChange(ctx context.Context, task *v1alpha1.AgentTask, _, newPhase v1alpha1.AgentTaskPhase) error {
	switch newPhase {
	case v1alpha1.AgentTaskPhaseProvisioning, v1alpha1.AgentTaskPhaseActive:
		return n.NotifyTaskStarted(ctx, task)
	case v1alpha1.AgentTaskPhaseCompleted:
		return n.NotifyTaskCompleted(ctx, task)
	case v1alpha1.AgentTaskPhaseFailed:
		reason := ""
		if task != nil {
			reason = task.Status.Message
		}
		return n.NotifyTaskFailed(ctx, task, reason)
	case v1alpha1.AgentTaskPhasePaused:
		reason := ""
		if task != nil {
			reason = task.Status.Message
		}
		return n.upsertTaskComment(ctx, task, "⏸️ Paused", reason)
	default:
		return nil
	}
}

// FormatAgentTaskComment formats the Markdown PR/MR comment for an AgentTask.
func (n *SCMTaskNotifier) FormatAgentTaskComment(task *v1alpha1.AgentTask, phaseBadge, reason string) string {
	return FormatAgentTaskComment(task, phaseBadge, reason)
}

// FormatAgentTaskComment formats a Markdown PR/MR comment representing an AgentTask's status,
// preview magic link, LLM token/cost usage, and interactive slash commands.
func FormatAgentTaskComment(task *v1alpha1.AgentTask, phaseBadge, reason string) string {
	if task == nil {
		return ""
	}

	if phaseBadge == "" {
		switch task.Status.Phase {
		case v1alpha1.AgentTaskPhaseCompleted:
			phaseBadge = "✅ Completed"
		case v1alpha1.AgentTaskPhaseFailed:
			phaseBadge = "❌ Failed"
		case v1alpha1.AgentTaskPhasePaused:
			phaseBadge = "⏸️ Paused"
		default:
			phaseBadge = "🚀 Running"
		}
	}

	workingBranch := strings.TrimSpace(task.Spec.Repository.WorkingBranch)
	if workingBranch == "" && task.Annotations != nil {
		workingBranch = strings.TrimSpace(task.Annotations[annotationPreviewBranch])
	}
	if workingBranch == "" {
		if task.Name != "" {
			workingBranch = "agent/" + task.Name
		} else {
			workingBranch = "main"
		}
	}

	previewCell := "⏳ Provisioning..."
	if task.Annotations != nil {
		if rawURL := strings.TrimSpace(task.Annotations[annotationPreviewURL]); rawURL != "" {
			envName := strings.TrimSpace(task.Annotations[annotationPreviewEnvironment])
			if envName == "" && task.Name != "" {
				envName = "agent-preview-" + task.Name
			}
			magicURL := buildMagicPreviewURL(rawURL, envName)
			exitURL := buildMagicPreviewURL(rawURL, "clear")
			previewCell = fmt.Sprintf("[🔗 Open Preview](%s) ([Exit Preview](%s))", sanitizeMarkdown(magicURL), sanitizeMarkdown(exitURL))
		}
	}

	costUSD := strings.TrimSpace(task.Status.CostUSD)
	if costUSD == "" {
		costUSD = "0.00"
	}
	costUSD = strings.TrimPrefix(costUSD, "$")
	llmUsage := fmt.Sprintf("%d tokens / $%s", task.Status.TokensConsumed, sanitizeMarkdown(costUSD))

	var b strings.Builder
	b.WriteString(AgentTaskCommentMarker + "\n")
	fmt.Fprintf(&b, "### 🤖 Diverge Autonomous Agent Task: `%s`\n\n", sanitizeMarkdown(task.Name))
	b.WriteString("| Field | Value |\n")
	b.WriteString("|-------|-------|\n")
	fmt.Fprintf(&b, "| **Status** | %s |\n", sanitizeMarkdown(phaseBadge))
	fmt.Fprintf(&b, "| **Objective** | %s |\n", sanitizeMarkdown(task.Spec.Objective))
	fmt.Fprintf(&b, "| **Working Branch** | `%s` |\n", sanitizeMarkdown(workingBranch))
	fmt.Fprintf(&b, "| **Linked Preview** | %s |\n", previewCell)
	fmt.Fprintf(&b, "| **LLM Usage** | %s |\n", llmUsage)
	if strings.TrimSpace(reason) != "" {
		fmt.Fprintf(&b, "| **Failure Reason** | %s |\n", sanitizeMarkdown(reason))
	}
	if strings.TrimSpace(task.Status.PRURL) != "" {
		fmt.Fprintf(&b, "| **Pull Request** | %s |\n", sanitizeMarkdown(task.Status.PRURL))
	}
	b.WriteString("\n💡 *Commands: `/diverge pause`, `/diverge resume`, or `/diverge fix <new objective>`*")
	return b.String()
}

func buildMagicPreviewURL(rawURL, envValue string) string {
	if envValue == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		sep := "?"
		if strings.Contains(rawURL, "?") {
			sep = "&"
		}
		return rawURL + sep + "_diverge_env=" + url.QueryEscape(envValue)
	}
	q := u.Query()
	q.Set("_diverge_env", envValue)
	u.RawQuery = q.Encode()
	return u.String()
}

func (n *SCMTaskNotifier) upsertTaskComment(ctx context.Context, task *v1alpha1.AgentTask, phaseBadge, reason string) error {
	if n == nil || n.Underlying == nil || task == nil {
		return nil
	}

	project, mrID := extractTaskProjectAndMR(task)
	if project == "" || mrID <= 0 {
		return nil
	}

	body := FormatAgentTaskComment(task, phaseBadge, reason)
	existingID, err := n.Underlying.FindExistingComment(ctx, project, mrID, AgentTaskCommentMarker)
	if err != nil {
		return err
	}
	if existingID > 0 {
		return n.Underlying.UpdateComment(ctx, project, mrID, existingID, body)
	}
	_, err = n.Underlying.PostComment(ctx, project, mrID, body)
	return err
}

func extractTaskProjectAndMR(task *v1alpha1.AgentTask) (string, int) {
	if task == nil {
		return "", 0
	}
	return extractTaskProject(task), extractTaskMRID(task)
}

func extractTaskMRID(task *v1alpha1.AgentTask) int {
	if task == nil {
		return 0
	}
	for _, m := range []map[string]string{task.Annotations, task.Labels} {
		if m == nil {
			continue
		}
		for _, key := range []string{annotationPRNumber, annotationMRNumber, annotationMRIID} {
			if raw := strings.TrimSpace(m[key]); raw != "" {
				if n, err := strconv.Atoi(raw); err == nil && n > 0 {
					return n
				}
			}
		}
	}

	for _, branch := range []string{
		task.Spec.Repository.WorkingBranch,
		task.Annotations[annotationPreviewBranch],
	} {
		if m := prBranchRegex.FindStringSubmatch(strings.TrimSpace(branch)); len(m) == 2 {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
				return n
			}
		}
	}

	if m := prURLRegex.FindStringSubmatch(strings.TrimSpace(task.Status.PRURL)); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return n
		}
	}

	return 0
}

func extractTaskProject(task *v1alpha1.AgentTask) string {
	if task == nil {
		return ""
	}
	for _, m := range []map[string]string{task.Annotations, task.Labels} {
		if m == nil {
			continue
		}
		if proj := strings.TrimSpace(m[annotationProject]); proj != "" {
			return strings.Trim(proj, "/")
		}
	}
	return parseProjectFromRepoURL(task.Spec.Repository.URL)
}

func parseProjectFromRepoURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}

	var pathPart string
	if strings.Contains(rawURL, "://") {
		u, err := url.Parse(rawURL)
		if err != nil || u.Host == "" {
			return ""
		}
		pathPart = u.Path
	} else if m := scpRepoRegex.FindStringSubmatch(rawURL); len(m) == 2 {
		pathPart = m[1]
	} else {
		return ""
	}

	pathPart = strings.Trim(pathPart, "/")
	pathPart = strings.TrimSuffix(pathPart, ".git")
	pathPart = strings.Trim(pathPart, "/")
	if !strings.Contains(pathPart, "/") {
		return ""
	}
	parts := strings.Split(pathPart, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return ""
		}
	}
	return pathPart
}

// FindExistingComment searches GitHub issue comments on a PR for a comment containing marker.
func (g *GitHubNotifier) FindExistingComment(ctx context.Context, project string, mrID int, marker string) (int64, error) {
	if marker == "" {
		marker = AgentTaskCommentMarker
	}
	escapedProject, err := escapeProjectPath(project)
	if err != nil {
		return 0, err
	}
	reqURL := fmt.Sprintf("%s/repos/%s/issues/%d/comments", g.BaseURL, escapedProject, mrID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := g.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("github API returned status: %d", resp.StatusCode)
	}

	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		return 0, err
	}
	for _, c := range comments {
		if strings.Contains(c.Body, marker) {
			return c.ID, nil
		}
	}
	return 0, nil
}

// PostComment creates a new issue comment on a GitHub PR and returns its comment ID.
func (g *GitHubNotifier) PostComment(ctx context.Context, project string, mrID int, body string) (int64, error) {
	escapedProject, err := escapeProjectPath(project)
	if err != nil {
		return 0, err
	}
	payload, _ := json.Marshal(map[string]string{"body": body})
	reqURL := fmt.Sprintf("%s/repos/%s/issues/%d/comments", g.BaseURL, escapedProject, mrID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := g.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("github API returned status: %d", resp.StatusCode)
	}
	var result struct {
		ID int64 `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	return result.ID, nil
}

// UpdateComment updates an existing issue comment on a GitHub PR.
func (g *GitHubNotifier) UpdateComment(ctx context.Context, project string, _ int, commentID int64, body string) error {
	escapedProject, err := escapeProjectPath(project)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"body": body})
	reqURL := fmt.Sprintf("%s/repos/%s/issues/comments/%d", g.BaseURL, escapedProject, commentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, reqURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := g.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github API returned status: %d", resp.StatusCode)
	}
	return nil
}

// FindExistingComment searches GitLab merge request notes for a note containing marker.
func (g *GitLabNotifier) FindExistingComment(ctx context.Context, project string, mrID int, marker string) (int64, error) {
	if marker == "" {
		marker = AgentTaskCommentMarker
	}
	escapedProject := url.PathEscape(project)
	reqURL := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d/notes", g.BaseURL, escapedProject, mrID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("PRIVATE-TOKEN", g.Token)

	client := g.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("gitlab API returned status: %d", resp.StatusCode)
	}

	var notes []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&notes); err != nil {
		return 0, err
	}
	for _, n := range notes {
		if strings.Contains(n.Body, marker) {
			return n.ID, nil
		}
	}
	return 0, nil
}

// PostComment creates a new merge request note on GitLab and returns its note ID.
func (g *GitLabNotifier) PostComment(ctx context.Context, project string, mrID int, body string) (int64, error) {
	escapedProject := url.PathEscape(project)
	payload, _ := json.Marshal(map[string]string{"body": body})
	reqURL := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d/notes", g.BaseURL, escapedProject, mrID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PRIVATE-TOKEN", g.Token)

	client := g.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("gitlab API returned status: %d", resp.StatusCode)
	}
	var result struct {
		ID int64 `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	return result.ID, nil
}

// UpdateComment updates an existing merge request note on GitLab.
func (g *GitLabNotifier) UpdateComment(ctx context.Context, project string, mrID int, commentID int64, body string) error {
	escapedProject := url.PathEscape(project)
	payload, _ := json.Marshal(map[string]string{"body": body})
	reqURL := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d/notes/%d", g.BaseURL, escapedProject, mrID, commentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PRIVATE-TOKEN", g.Token)

	client := g.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gitlab API returned status: %d", resp.StatusCode)
	}
	return nil
}
