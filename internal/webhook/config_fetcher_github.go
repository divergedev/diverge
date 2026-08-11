package webhook

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// GitHubConfigFetcher fetches .diverge.yaml from the GitHub API.
type GitHubConfigFetcher struct {
	Token      string
	HTTPClient *http.Client
}

func (f *GitHubConfigFetcher) FetchConfig(ctx context.Context, provider, project, ref string) ([]byte, error) {
	// GET /repos/:owner/:repo/contents/.diverge.yaml?ref=:ref
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/.diverge.yaml?ref=%s",
		project, url.QueryEscape(ref))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+f.Token)
	req.Header.Set("Accept", "application/vnd.github.raw+json")

	resp, err := f.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch .diverge.yaml: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf(".diverge.yaml not found in %s@%s", project, ref)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching .diverge.yaml", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
