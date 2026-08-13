package webhook

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/divergedev/diverge/internal/config"
)

// GitLabConfigFetcher fetches .diverge.yaml from the GitLab API.
type GitLabConfigFetcher struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func (f *GitLabConfigFetcher) FetchConfig(ctx context.Context, provider, project, ref string) ([]byte, error) {
	baseURL := f.BaseURL
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}

	// GET /api/v4/projects/:id/repository/files/:file_path/raw?ref=:ref
	apiURL := fmt.Sprintf("%s/api/v4/projects/%s/repository/files/%s/raw?ref=%s",
		baseURL,
		url.PathEscape(project),
		url.PathEscape(".diverge.yaml"),
		url.QueryEscape(ref),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("PRIVATE-TOKEN", f.Token)

	resp, err := f.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch .diverge.yaml: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, config.ErrConfigNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching .diverge.yaml", resp.StatusCode)
	}

	const maxConfigSize = 1 << 20 // 1 MiB
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxConfigSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	if int64(len(data)) > maxConfigSize {
		return nil, fmt.Errorf("config file exceeds maximum size of %d bytes", maxConfigSize)
	}
	return data, nil
}
