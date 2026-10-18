package agentrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultTaskNamespace       = "default"
	DefaultBaseBranch          = "main"
	DefaultMaxIterations       = 5
	DefaultTokenFile           = "/etc/diverge/token/token"
	DefaultMCPURL              = "http://diverge-server.diverge-system.svc:8080/mcp"
	DefaultMeterURL            = "http://diverge-server.diverge-system.svc:8080/api/v1/agenttask/meter"
	DefaultPreviewHeaderKey    = "x-diverge-env"
	DefaultWorkspaceDir        = "/workspace"
	DefaultModel               = "claude-3-7-sonnet"
	DefaultTokensPerIteration  = int64(2500)
	DefaultCostPerIterationUSD = 0.05
)

var (
	// ErrBudgetExceeded is returned when the LLM metering endpoint reports that the task has exceeded its token or USD budget.
	ErrBudgetExceeded = errors.New("agent task budget exceeded")
	// ErrModelDisallowed is returned when the LLM metering endpoint rejects the requested model under the Macaroon token caveats.
	ErrModelDisallowed = errors.New("agent task model disallowed")
	// ErrMaxIterationsReached is returned when the agent loop exhausts MaxIterations without passing verification.
	ErrMaxIterationsReached = errors.New("max iterations reached without passing verification")
)

// Config holds the runtime configuration for the autonomous agent runner.
type Config struct {
	TaskName            string
	TaskNamespace       string
	Objective           string
	RepoURL             string
	BaseBranch          string
	WorkingBranch       string
	BudgetUSD           string
	MaxIterations       int
	TokenFile           string
	MCPURL              string
	MeterURL            string
	PreviewURL          string
	PreviewHeaderKey    string
	PreviewHeaderValue  string
	WorkspaceDir        string
	AgentCommand        string
	VerifyCommand       string
	Guidance            string
	Model               string
	TokensPerIteration  int64
	CostPerIterationUSD float64
}

// LoadConfigFromEnv parses and validates the agent runner configuration from environment variables.
func LoadConfigFromEnv(getenv func(string) string) (*Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	taskName := strings.TrimSpace(getenv("DIVERGE_TASK_NAME"))
	if taskName == "" {
		return nil, errors.New("DIVERGE_TASK_NAME is required")
	}

	objective := strings.TrimSpace(getenv("DIVERGE_OBJECTIVE"))
	if objective == "" {
		return nil, errors.New("DIVERGE_OBJECTIVE is required")
	}

	repoURL := strings.TrimSpace(getenv("DIVERGE_REPO_URL"))
	if repoURL == "" {
		return nil, errors.New("DIVERGE_REPO_URL is required")
	}

	taskNamespace := strings.TrimSpace(getenv("DIVERGE_TASK_NAMESPACE"))
	if taskNamespace == "" {
		taskNamespace = DefaultTaskNamespace
	}

	baseBranch := strings.TrimSpace(getenv("DIVERGE_BASE_BRANCH"))
	if baseBranch == "" {
		baseBranch = DefaultBaseBranch
	}

	workingBranch := strings.TrimSpace(getenv("DIVERGE_WORKING_BRANCH"))
	if workingBranch == "" {
		workingBranch = fmt.Sprintf("agent/%s", taskName)
	}

	maxIterations := DefaultMaxIterations
	if rawMax := strings.TrimSpace(getenv("DIVERGE_MAX_ITERATIONS")); rawMax != "" {
		parsed, err := strconv.Atoi(rawMax)
		if err != nil {
			return nil, fmt.Errorf("invalid DIVERGE_MAX_ITERATIONS %q: %w", rawMax, err)
		}
		if parsed > 0 {
			maxIterations = parsed
		}
	}

	tokenFile := strings.TrimSpace(getenv("DIVERGE_TOKEN_FILE"))
	if tokenFile == "" {
		tokenFile = DefaultTokenFile
	}

	mcpURL := strings.TrimSpace(getenv("DIVERGE_MCP_URL"))
	if mcpURL == "" {
		mcpURL = DefaultMCPURL
	}

	meterURL := strings.TrimSpace(getenv("DIVERGE_METER_URL"))
	if meterURL == "" {
		meterURL = DefaultMeterURL
	}

	previewHeaderKey := strings.TrimSpace(getenv("DIVERGE_PREVIEW_HEADER_KEY"))
	if previewHeaderKey == "" {
		previewHeaderKey = DefaultPreviewHeaderKey
	}

	workspaceDir := strings.TrimSpace(getenv("DIVERGE_WORKSPACE_DIR"))
	if workspaceDir == "" {
		workspaceDir = DefaultWorkspaceDir
	}

	model := strings.TrimSpace(getenv("DIVERGE_MODEL"))
	if model == "" {
		model = DefaultModel
	}

	tokensPerIteration := DefaultTokensPerIteration
	if rawTokens := strings.TrimSpace(getenv("DIVERGE_TOKENS_PER_ITERATION")); rawTokens != "" {
		parsed, err := strconv.ParseInt(rawTokens, 10, 64)
		if err != nil || parsed < 0 {
			return nil, fmt.Errorf("invalid DIVERGE_TOKENS_PER_ITERATION %q", rawTokens)
		}
		tokensPerIteration = parsed
	}

	costPerIterationUSD := DefaultCostPerIterationUSD
	if rawCost := strings.TrimSpace(getenv("DIVERGE_COST_PER_ITERATION_USD")); rawCost != "" {
		parsed, err := strconv.ParseFloat(rawCost, 64)
		if err != nil || parsed < 0 {
			return nil, fmt.Errorf("invalid DIVERGE_COST_PER_ITERATION_USD %q", rawCost)
		}
		costPerIterationUSD = parsed
	}

	return &Config{
		TaskName:            taskName,
		TaskNamespace:       taskNamespace,
		Objective:           objective,
		RepoURL:             repoURL,
		BaseBranch:          baseBranch,
		WorkingBranch:       workingBranch,
		BudgetUSD:           strings.TrimSpace(getenv("DIVERGE_BUDGET_USD")),
		MaxIterations:       maxIterations,
		TokenFile:           tokenFile,
		MCPURL:              mcpURL,
		MeterURL:            meterURL,
		PreviewURL:          strings.TrimSpace(getenv("DIVERGE_PREVIEW_URL")),
		PreviewHeaderKey:    previewHeaderKey,
		PreviewHeaderValue:  strings.TrimSpace(getenv("DIVERGE_PREVIEW_HEADER_VALUE")),
		WorkspaceDir:        workspaceDir,
		AgentCommand:        strings.TrimSpace(getenv("DIVERGE_AGENT_CMD")),
		VerifyCommand:       strings.TrimSpace(getenv("DIVERGE_VERIFY_CMD")),
		Guidance:            strings.TrimSpace(getenv("DIVERGE_GUIDANCE")),
		Model:               model,
		TokensPerIteration:  tokensPerIteration,
		CostPerIterationUSD: costPerIterationUSD,
	}, nil
}

// MeterRequest matches internal/server.LLMMeterRequest JSON schema for POST /api/v1/agenttask/meter.
type MeterRequest struct {
	RequestID  string  `json:"requestId,omitempty"`
	Iteration  int32   `json:"iteration,omitempty"`
	Model      string  `json:"model"`
	TokensUsed int64   `json:"tokensUsed"`
	CostUSD    float64 `json:"costUSD"`
}

// MeterResponse matches internal/server.LLMMeterResponse JSON schema returned by POST /api/v1/agenttask/meter.
type MeterResponse struct {
	Allowed         bool    `json:"allowed"`
	Suspended       bool    `json:"suspended"`
	Reason          string  `json:"reason,omitempty"`
	TotalTokensUsed int64   `json:"totalTokensUsed"`
	TotalCostUSD    float64 `json:"totalCostUSD"`
	Iteration       int32   `json:"iteration,omitempty"`
	MaxIterations   int32   `json:"maxIterations,omitempty"`
	Guidance        string  `json:"guidance,omitempty"`
}

// CommandExecutor abstracts external command execution for testability.
type CommandExecutor interface {
	Run(ctx context.Context, dir string, env []string, name string, args ...string) (string, error)
}

// OSCommandExecutor runs commands on the host OS via os/exec.
type OSCommandExecutor struct{}

// Run executes a command with context, working directory, and additional environment variables.
func (e OSCommandExecutor) Run(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("command %s %s failed: %w (output: %s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// RunResult summarizes the outcome of an autonomous agent runner execution.
type RunResult struct {
	Succeeded       bool
	Iterations      int
	Committed       bool
	WorkingBranch   string
	TotalTokensUsed int64
	TotalCostUSD    float64
}

// Runner orchestrates the autonomous agent loop inside a Diverge sandbox pod.
type Runner struct {
	Config     *Config
	Exec       CommandExecutor
	HTTPClient *http.Client
	Logger     io.Writer
}

// NewRunner constructs a Runner with default OSCommandExecutor and HTTPClient.
func NewRunner(cfg *Config) *Runner {
	return &Runner{
		Config: cfg,
		Exec:   OSCommandExecutor{},
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		Logger: os.Stdout,
	}
}

func (r *Runner) executor() CommandExecutor {
	if r.Exec != nil {
		return r.Exec
	}
	return OSCommandExecutor{}
}

func (r *Runner) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (r *Runner) logf(format string, args ...any) {
	w := r.Logger
	if w == nil {
		w = io.Discard
	}
	_, _ = fmt.Fprintf(w, format+"\n", args...)
}

// ResolveToken loads the Macaroon capability token from DIVERGE_MACAROON_TOKEN or Config.TokenFile.
func (r *Runner) ResolveToken() (string, error) {
	if envToken := strings.TrimSpace(os.Getenv("DIVERGE_MACAROON_TOKEN")); envToken != "" {
		return envToken, nil
	}
	if r.Config == nil || r.Config.TokenFile == "" {
		return "", errors.New("token file path not configured")
	}
	data, err := os.ReadFile(r.Config.TokenFile)
	if err != nil {
		return "", fmt.Errorf("read capability token from %s: %w", r.Config.TokenFile, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("capability token file %s is empty", r.Config.TokenFile)
	}
	return token, nil
}

// ReportUsage reports token and cost consumption for an iteration to the Diverge metering endpoint.
func (r *Runner) ReportUsage(ctx context.Context, token, model string, tokensUsed int64, costUSD float64) (*MeterResponse, error) {
	return r.ReportUsageForIteration(ctx, 0, token, model, tokensUsed, costUSD)
}

// ReportUsageForIteration reports token, cost, and iteration consumption with a deterministic idempotency RequestID.
func (r *Runner) ReportUsageForIteration(ctx context.Context, iter int, token, model string, tokensUsed int64, costUSD float64) (*MeterResponse, error) {
	var reqID string
	if iter > 0 && r.Config != nil && r.Config.TaskName != "" {
		reqID = fmt.Sprintf("%s-iter-%d", r.Config.TaskName, iter)
	}
	payload := MeterRequest{
		RequestID:  reqID,
		Iteration:  int32(iter),
		Model:      model,
		TokensUsed: tokensUsed,
		CostUSD:    costUSD,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal meter request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Config.MeterURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create meter request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("post meter usage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var meterResp MeterResponse
	if err := json.NewDecoder(resp.Body).Decode(&meterResp); err != nil {
		return nil, fmt.Errorf("decode meter response (status %d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode == http.StatusForbidden {
		return &meterResp, fmt.Errorf("%w: %s", ErrModelDisallowed, meterResp.Reason)
	}

	if resp.StatusCode == http.StatusTooManyRequests || !meterResp.Allowed || meterResp.Suspended {
		return &meterResp, fmt.Errorf("%w: %s", ErrBudgetExceeded, meterResp.Reason)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &meterResp, fmt.Errorf("unexpected meter status %d: %s", resp.StatusCode, meterResp.Reason)
	}

	return &meterResp, nil
}

// CheckPreviewHealth probes the ephemeral preview URL (if configured) with the Diverge isolation header.
func (r *Runner) CheckPreviewHealth(ctx context.Context) error {
	if r.Config == nil || r.Config.PreviewURL == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.Config.PreviewURL, nil)
	if err != nil {
		return fmt.Errorf("create preview health request: %w", err)
	}
	req.Header.Set("Accept", "application/json, text/html")
	if r.Config.PreviewHeaderKey != "" && r.Config.PreviewHeaderValue != "" {
		req.Header.Set(r.Config.PreviewHeaderKey, r.Config.PreviewHeaderValue)
	}

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("preview health request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return nil
	}
	return fmt.Errorf("preview health check returned HTTP %d", resp.StatusCode)
}

// Run executes the end-to-end autonomous agent workflow: token resolution, repo clone,
// iterative metered execution + verification, and git commit/push upon passing verification.
func (r *Runner) Run(ctx context.Context) (*RunResult, error) {
	if r.Config == nil {
		return nil, errors.New("runner config is nil")
	}

	token, err := r.ResolveToken()
	if err != nil {
		return nil, err
	}

	exec := r.executor()

	r.logf("cloning repository %s (branch %s) into %s", r.Config.RepoURL, r.Config.BaseBranch, r.Config.WorkspaceDir)
	if _, err := exec.Run(ctx, "", nil, "git", "clone", "--depth=50", "--branch", r.Config.BaseBranch, r.Config.RepoURL, r.Config.WorkspaceDir); err != nil {
		return nil, fmt.Errorf("git clone: %w", err)
	}

	r.logf("checking out working branch %s", r.Config.WorkingBranch)
	if _, err := exec.Run(ctx, r.Config.WorkspaceDir, nil, "git", "checkout", "-B", r.Config.WorkingBranch); err != nil {
		return nil, fmt.Errorf("git checkout working branch: %w", err)
	}

	var (
		lastTokens  int64
		lastCost    float64
		lastFailure string
	)

	for iter := 1; iter <= r.Config.MaxIterations; iter++ {
		r.logf("starting iteration %d/%d", iter, r.Config.MaxIterations)

		if latestToken, tokErr := r.ResolveToken(); tokErr == nil && latestToken != "" {
			token = latestToken
		}

		meterResp, err := r.ReportUsageForIteration(ctx, iter, token, r.Config.Model, r.Config.TokensPerIteration, r.Config.CostPerIterationUSD)
		if meterResp != nil {
			lastTokens = meterResp.TotalTokensUsed
			lastCost = meterResp.TotalCostUSD
			if liveGuidance := strings.TrimSpace(meterResp.Guidance); liveGuidance != "" {
				r.Config.Guidance = liveGuidance
				if r.Config.WorkspaceDir != "" {
					_ = os.WriteFile(filepath.Join(r.Config.WorkspaceDir, ".diverge-guidance.md"), []byte(liveGuidance+"\n"), 0600)
				}
			}
		}
		failedResult := func() *RunResult {
			return &RunResult{
				Succeeded:       false,
				Iterations:      iter,
				WorkingBranch:   r.Config.WorkingBranch,
				TotalTokensUsed: lastTokens,
				TotalCostUSD:    lastCost,
			}
		}
		if err != nil {
			return failedResult(), err
		}

		if r.Config.AgentCommand != "" {
			agentEnv := []string{
				fmt.Sprintf("DIVERGE_ITERATION=%d", iter),
				fmt.Sprintf("DIVERGE_OBJECTIVE=%s", r.Config.Objective),
				fmt.Sprintf("DIVERGE_PREVIEW_URL=%s", r.Config.PreviewURL),
				fmt.Sprintf("DIVERGE_PREVIEW_HEADER_KEY=%s", r.Config.PreviewHeaderKey),
				fmt.Sprintf("DIVERGE_PREVIEW_HEADER_VALUE=%s", r.Config.PreviewHeaderValue),
				fmt.Sprintf("DIVERGE_MCP_URL=%s", r.Config.MCPURL),
			}
			if lastFailure != "" {
				agentEnv = append(agentEnv, fmt.Sprintf("DIVERGE_LAST_FAILURE=%s", lastFailure))
			}
			if r.Config.Guidance != "" {
				agentEnv = append(agentEnv, fmt.Sprintf("DIVERGE_GUIDANCE=%s", r.Config.Guidance))
			}
			if _, err := exec.Run(ctx, r.Config.WorkspaceDir, agentEnv, "sh", "-c", r.Config.AgentCommand); err != nil {
				return failedResult(), fmt.Errorf("agent command failed on iteration %d: %w", iter, err)
			}
		}

		if err := r.CheckPreviewHealth(ctx); err != nil {
			lastFailure = err.Error()
			r.logf("iteration %d preview health check failed: %v", iter, err)
			continue
		}

		if r.Config.VerifyCommand != "" {
			verifyEnv := []string{
				fmt.Sprintf("DIVERGE_PREVIEW_URL=%s", r.Config.PreviewURL),
				fmt.Sprintf("DIVERGE_PREVIEW_HEADER_KEY=%s", r.Config.PreviewHeaderKey),
				fmt.Sprintf("DIVERGE_PREVIEW_HEADER_VALUE=%s", r.Config.PreviewHeaderValue),
			}
			if verifyOut, err := exec.Run(ctx, r.Config.WorkspaceDir, verifyEnv, "sh", "-c", r.Config.VerifyCommand); err != nil {
				lastFailure = strings.TrimSpace(verifyOut)
				if lastFailure == "" {
					lastFailure = err.Error()
				}
				r.logf("iteration %d verification failed: %v", iter, err)
				continue
			}
		}

		statusRaw, err := exec.Run(ctx, r.Config.WorkspaceDir, nil, "git", "status", "--porcelain")
		if err != nil {
			return failedResult(), fmt.Errorf("git status: %w", err)
		}

		statusOut := strings.TrimSpace(statusRaw)
		if statusOut != "" {
			if _, err := exec.Run(ctx, r.Config.WorkspaceDir, nil, "git", "add", "-A"); err != nil {
				return failedResult(), fmt.Errorf("git add: %w", err)
			}
			commitMsg := fmt.Sprintf("fix(%s): %s", r.Config.TaskName, r.Config.Objective)
			if _, err := exec.Run(ctx, r.Config.WorkspaceDir, nil, "git", "commit", "-m", commitMsg); err != nil {
				return failedResult(), fmt.Errorf("git commit: %w", err)
			}
			if _, err := exec.Run(ctx, r.Config.WorkspaceDir, nil, "git", "push", "-u", "origin", r.Config.WorkingBranch); err != nil {
				return failedResult(), fmt.Errorf("git push: %w", err)
			}
		}

		return &RunResult{
			Succeeded:       true,
			Iterations:      iter,
			Committed:       statusOut != "",
			WorkingBranch:   r.Config.WorkingBranch,
			TotalTokensUsed: lastTokens,
			TotalCostUSD:    lastCost,
		}, nil
	}

	return &RunResult{
		Succeeded:       false,
		Iterations:      r.Config.MaxIterations,
		WorkingBranch:   r.Config.WorkingBranch,
		TotalTokensUsed: lastTokens,
		TotalCostUSD:    lastCost,
	}, ErrMaxIterationsReached
}
