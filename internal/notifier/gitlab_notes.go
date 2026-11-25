package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type gitlabNotesClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func resolveGitLabAuthor(ctx context.Context, c gitlabNotesClient, explicitUsername string, cache *cachedNotifierAuthor) (int64, string, error) {
	if id, username, needFetch := cache.get(explicitUsername); !needFetch {
		return id, username, nil
	}
	baseURL := strings.TrimRight(strings.TrimSpace(c.baseURL), "/")
	if baseURL == "" {
		return 0, "", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v4/user", nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("gitlab /api/v4/user lookup failed: %w", err)
	}
	if resp.StatusCode == http.StatusForbidden &&
		(resp.Header.Get("Retry-After") != "" || resp.Header.Get("RateLimit-Remaining") == "0") {
		_ = resp.Body.Close()
		return 0, "", fmt.Errorf("gitlab /api/v4/user lookup rate limited: status %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		cache.markChecked()
		return 0, "", nil
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return 0, "", fmt.Errorf("gitlab /api/v4/user lookup returned status: %d", resp.StatusCode)
	}
	var u struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	decErr := json.NewDecoder(resp.Body).Decode(&u)
	_ = resp.Body.Close()
	if decErr != nil {
		return 0, "", fmt.Errorf("decoding gitlab /api/v4/user response: %w", decErr)
	}
	if username := strings.TrimSpace(u.Username); username != "" {
		cache.record(u.ID, username)
		return u.ID, username, nil
	}
	cache.markChecked()
	return 0, "", nil
}

func findGitLabNoteID(ctx context.Context, c gitlabNotesClient, explicitUsername string, cache *cachedNotifierAuthor, escapedProject string, mr int, marker string) (int64, error) {
	if marker == "" {
		return 0, nil
	}
	authID, authUsername, err := resolveGitLabAuthor(ctx, c, explicitUsername, cache)
	if err != nil {
		return 0, err
	}
	if authID == 0 && authUsername == "" {
		return 0, nil
	}
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	nextURL := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d/notes?per_page=100&sort=desc&order_by=updated_at", c.baseURL, escapedProject, mr)
	for page := 0; page < 5 && nextURL != ""; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("PRIVATE-TOKEN", c.token)
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, fmt.Errorf("gitlab note lookup failed: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return 0, fmt.Errorf("gitlab note lookup returned status: %d", resp.StatusCode)
		}
		var notes []struct {
			ID     int64  `json:"id"`
			Body   string `json:"body"`
			Author struct {
				ID       int64  `json:"id"`
				Username string `json:"username"`
			} `json:"author"`
		}
		decErr := json.NewDecoder(resp.Body).Decode(&notes)
		linkHeader := resp.Header.Get("Link")
		_ = resp.Body.Close()
		if decErr != nil {
			return 0, fmt.Errorf("decoding gitlab note lookup response: %w", decErr)
		}
		for _, n := range notes {
			if n.ID != 0 && notifierAuthorMatches(authID, authUsername, n.Author.ID, n.Author.Username) && strings.Contains(n.Body, marker) {
				return n.ID, nil
			}
		}
		nextURL = parseNextLink(linkHeader)
	}
	return 0, nil
}
