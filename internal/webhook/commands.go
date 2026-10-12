package webhook

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultExtendTTLDuration = 24 * time.Hour
	prNumberAnnotationKey    = "divergedev.com/pr-number"
)

// SlashCommand represents a parsed /diverge command from a PR or MR comment.
type SlashCommand struct {
	Action    string
	Objective string
	Duration  time.Duration
}

// ParseSlashCommand parses a PR/MR comment body for a /diverge slash command.
// Supported commands:
//   - /diverge fix <objective>
//   - /diverge preview
//   - /diverge teardown
//   - /diverge extend-ttl [duration]
//   - /diverge pause
//   - /diverge resume
func ParseSlashCommand(commentBody string) (*SlashCommand, bool) {
	lines := strings.Split(commentBody, "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "/diverge") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "/diverge"))
		if rest == "" {
			continue
		}
		fields := strings.Fields(rest)
		subcmd := strings.ToLower(fields[0])
		argStr := strings.TrimSpace(rest[len(fields[0]):])

		switch subcmd {
		case "fix":
			if argStr == "" {
				return nil, false
			}
			return &SlashCommand{
				Action:    "fix",
				Objective: argStr,
			}, true
		case "preview":
			return &SlashCommand{
				Action: "preview",
			}, true
		case "teardown":
			return &SlashCommand{
				Action: "teardown",
			}, true
		case "extend-ttl":
			dur := defaultExtendTTLDuration
			if argStr != "" {
				parsed, err := time.ParseDuration(fields[1])
				if err != nil || parsed <= 0 {
					return nil, false
				}
				dur = parsed
			}
			return &SlashCommand{
				Action:   "extend-ttl",
				Duration: dur,
			}, true
		case "pause":
			return &SlashCommand{
				Action: "pause",
			}, true
		case "resume":
			return &SlashCommand{
				Action: "resume",
			}, true
		}
	}
	return nil, false
}

// ExecuteSlashCommand executes a parsed /diverge slash command against the Kubernetes cluster.
func ExecuteSlashCommand(ctx context.Context, c client.Client, ns string, cmd *SlashCommand, repoURL, branch string, prNumber int) error {
	if cmd == nil {
		return fmt.Errorf("slash command must not be nil")
	}
	if ns == "" {
		ns = "default"
	}
	if branch == "" {
		branch = "main"
	}

	switch cmd.Action {
	case "fix":
		return executeFixCommand(ctx, c, ns, cmd, repoURL, branch, prNumber)
	case "preview":
		return executePreviewCommand(ctx, c, ns, repoURL, branch, prNumber)
	case "teardown":
		return executeTeardownCommand(ctx, c, ns, repoURL, prNumber)
	case "extend-ttl":
		return executeExtendTTLCommand(ctx, c, ns, cmd.Duration, repoURL, prNumber)
	case "pause":
		return executeSuspendTaskCommand(ctx, c, ns, repoURL, prNumber, true)
	case "resume":
		return executeSuspendTaskCommand(ctx, c, ns, repoURL, prNumber, false)
	default:
		return fmt.Errorf("unsupported slash command action %q", cmd.Action)
	}
}

func executeFixCommand(ctx context.Context, c client.Client, ns string, cmd *SlashCommand, repoURL, branch string, prNumber int) error {
	if strings.TrimSpace(cmd.Objective) == "" {
		return fmt.Errorf("fix command requires a non-empty objective")
	}
	if repoURL == "" {
		repoURL = "https://github.com/unknown/repository"
	}

	taskName := buildCommandTaskName(repoURL, prNumber, cmd.Objective)
	prStr := strconv.Itoa(prNumber)
	workingBranch := fmt.Sprintf("agent/%s", taskName)
	if prNumber > 0 {
		workingBranch = fmt.Sprintf("pr-%d", prNumber)
	}

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskName,
			Namespace: ns,
		},
	}

	_, err := ctrl.CreateOrUpdate(ctx, c, task, func() error {
		if task.Labels == nil {
			task.Labels = make(map[string]string)
		}
		if task.Annotations == nil {
			task.Annotations = make(map[string]string)
		}
		task.Labels["divergedev.com/managed-by"] = "diverge-webhook"
		if prNumber > 0 {
			task.Labels[prNumberAnnotationKey] = prStr
			task.Annotations[prNumberAnnotationKey] = prStr
		}
		task.Spec.Objective = cmd.Objective
		task.Spec.Repository = v1alpha1.AgentTaskRepository{
			URL:           repoURL,
			BaseBranch:    branch,
			WorkingBranch: workingBranch,
		}
		return nil
	})
	return err
}

func executePreviewCommand(ctx context.Context, c client.Client, ns, repoURL, branch string, prNumber int) error {
	project := projectFromRepoURL(repoURL)
	provider := providerFromRepoURL(repoURL)
	prefix := "pr"
	if provider == "gitlab" {
		prefix = "mr"
	}
	envName := previewEnvName(prefix, project, prNumber)
	prStr := strconv.Itoa(prNumber)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      envName,
			Namespace: ns,
		},
	}

	_, err := ctrl.CreateOrUpdate(ctx, c, env, func() error {
		if env.Labels == nil {
			env.Labels = make(map[string]string)
		}
		if env.Annotations == nil {
			env.Annotations = make(map[string]string)
		}
		if prNumber > 0 {
			env.Labels[prNumberAnnotationKey] = prStr
			env.Annotations[prNumberAnnotationKey] = prStr
		}
		env.Spec.Source = v1alpha1.EnvironmentSource{
			Provider: provider,
			Project:  project,
			MR:       prNumber,
			Branch:   branch,
		}
		return nil
	})
	return err
}

func executeTeardownCommand(ctx context.Context, c client.Client, ns, repoURL string, prNumber int) error {
	envs, err := findMatchingEnvironments(ctx, c, ns, repoURL, prNumber)
	if err != nil {
		return err
	}
	for i := range envs {
		if err := client.IgnoreNotFound(c.Delete(ctx, &envs[i])); err != nil {
			return err
		}
	}

	pgs, err := findMatchingPreviewGroups(ctx, c, repoURL, prNumber)
	if err != nil {
		return err
	}
	for i := range pgs {
		if err := client.IgnoreNotFound(c.Delete(ctx, &pgs[i])); err != nil {
			return err
		}
	}
	return nil
}

func executeExtendTTLCommand(ctx context.Context, c client.Client, ns string, dur time.Duration, repoURL string, prNumber int) error {
	if dur <= 0 {
		dur = defaultExtendTTLDuration
	}

	envs, err := findMatchingEnvironments(ctx, c, ns, repoURL, prNumber)
	if err != nil {
		return err
	}
	for i := range envs {
		env := &envs[i]
		env.Spec.Lifecycle.TTL = &metav1.Duration{Duration: dur}
		if err := c.Update(ctx, env); err != nil {
			return err
		}
	}

	pgs, err := findMatchingPreviewGroups(ctx, c, repoURL, prNumber)
	if err != nil {
		return err
	}
	for i := range pgs {
		pg := &pgs[i]
		if pg.Spec.Lifecycle == nil {
			pg.Spec.Lifecycle = &v1alpha1.PreviewGroupLifecycle{}
		}
		pg.Spec.Lifecycle.TTL = &metav1.Duration{Duration: dur}
		if err := c.Update(ctx, pg); err != nil {
			return err
		}
	}
	return nil
}

func executeSuspendTaskCommand(ctx context.Context, c client.Client, ns, repoURL string, prNumber int, suspend bool) error {
	var taskList v1alpha1.AgentTaskList
	if err := c.List(ctx, &taskList, client.InNamespace(ns)); err != nil {
		return err
	}

	prStr := strconv.Itoa(prNumber)
	for i := range taskList.Items {
		task := &taskList.Items[i]
		if matchesTaskPR(task, repoURL, prNumber, prStr) {
			task.Spec.Suspended = suspend
			if err := c.Update(ctx, task); err != nil {
				return err
			}
		}
	}
	return nil
}

func matchesTaskPR(task *v1alpha1.AgentTask, repoURL string, prNumber int, prStr string) bool {
	if prNumber > 0 {
		if task.Annotations[prNumberAnnotationKey] == prStr || task.Labels[prNumberAnnotationKey] == prStr {
			return true
		}
		if task.Spec.Repository.WorkingBranch == fmt.Sprintf("pr-%d", prNumber) {
			return true
		}
	}
	if repoURL != "" && task.Spec.Repository.URL == repoURL && prNumber == 0 {
		return true
	}
	return false
}

func findMatchingEnvironments(ctx context.Context, c client.Client, ns, repoURL string, prNumber int) ([]v1alpha1.Environment, error) {
	var envList v1alpha1.EnvironmentList
	if err := c.List(ctx, &envList, client.InNamespace(ns)); err != nil {
		return nil, err
	}

	project := projectFromRepoURL(repoURL)
	expectedPRName := previewEnvName("pr", project, prNumber)
	expectedMRName := previewEnvName("mr", project, prNumber)
	prStr := strconv.Itoa(prNumber)

	var matched []v1alpha1.Environment
	for _, env := range envList.Items {
		if prNumber > 0 && (env.Spec.Source.MR == prNumber ||
			env.Annotations[prNumberAnnotationKey] == prStr ||
			env.Labels[prNumberAnnotationKey] == prStr ||
			env.Name == expectedPRName ||
			env.Name == expectedMRName) {
			matched = append(matched, env)
		}
	}
	return matched, nil
}

func findMatchingPreviewGroups(ctx context.Context, c client.Client, repoURL string, prNumber int) ([]v1alpha1.PreviewGroup, error) {
	var pgList v1alpha1.PreviewGroupList
	if err := c.List(ctx, &pgList); err != nil {
		// Ignore scheme error if PreviewGroup is not registered in a minimal test scheme
		return nil, nil
	}

	prStr := strconv.Itoa(prNumber)
	var matched []v1alpha1.PreviewGroup
	for _, pg := range pgList.Items {
		if prNumber > 0 && (pg.Spec.Source.MR == prNumber ||
			pg.Annotations[prNumberAnnotationKey] == prStr ||
			pg.Labels[prNumberAnnotationKey] == prStr) {
			matched = append(matched, pg)
		}
	}
	return matched, nil
}

func buildCommandTaskName(repoURL string, prNumber int, objective string) string {
	raw := fmt.Sprintf("task-pr-%d", prNumber)
	if prNumber <= 0 {
		raw = "task-fix"
	}
	raw = strings.ToLower(raw)
	raw = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(raw, "-")
	raw = strings.Trim(raw, "-")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%s", repoURL, prNumber, objective))))[:8]
	return fmt.Sprintf("%s-%s", raw, hash)
}

func projectFromRepoURL(repoURL string) string {
	trimmed := strings.TrimSpace(repoURL)
	trimmed = strings.TrimSuffix(trimmed, ".git")
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" {
		return strings.Trim(parsed.Path, "/")
	}
	return strings.Trim(trimmed, "/")
}

func providerFromRepoURL(repoURL string) string {
	lower := strings.ToLower(repoURL)
	if strings.Contains(lower, "gitlab") {
		return "gitlab"
	}
	return "github"
}
