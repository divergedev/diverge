package features

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrUnleashNotFound is returned when a requested Unleash resource is not found (HTTP 404).
var ErrUnleashNotFound = errors.New("unleash resource not found")

// UnleashClient provides a lightweight HTTP client for interacting with Unleash Admin API.
type UnleashClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewUnleashClient creates a new UnleashClient with a bounded HTTP timeout.
func NewUnleashClient(baseURL, token string, customHTTPClient *http.Client) (*UnleashClient, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid unleash url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid unleash url scheme %q (must be http or https)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("invalid unleash url host cannot be empty")
	}

	client := customHTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	return &UnleashClient{
		baseURL:    trimmed,
		token:      strings.TrimSpace(token),
		httpClient: client,
	}, nil
}

func (c *UnleashClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, int, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	fullURL := fmt.Sprintf("%s%s", c.baseURL, path)
	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("unleash request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response body: %w", err)
	}

	return respData, resp.StatusCode, nil
}

// HealthCheck verifies connectivity and authentication with Unleash.
func (c *UnleashClient) HealthCheck(ctx context.Context) error {
	respData, statusCode, err := c.doRequest(ctx, http.MethodGet, "/api/health", nil)
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK {
		return fmt.Errorf("unleash health check failed (status %d): %s", statusCode, string(respData))
	}
	return nil
}

// CreateEnvironment idempotently creates a new environment in Unleash.
func (c *UnleashClient) CreateEnvironment(ctx context.Context, name, envType string) error {
	payload := map[string]interface{}{
		"name": name,
		"type": envType,
	}

	respData, statusCode, err := c.doRequest(ctx, http.MethodPost, "/api/admin/environments", payload)
	if err != nil {
		return err
	}

	if statusCode == http.StatusOK || statusCode == http.StatusCreated {
		return nil
	}
	// If already exists (409 Conflict), treat as success
	if statusCode == http.StatusConflict {
		return nil
	}

	return fmt.Errorf("failed to create unleash environment %s (status %d): %s", name, statusCode, string(respData))
}

// GetEnvironment retrieves an environment by name from Unleash.
func (c *UnleashClient) GetEnvironment(ctx context.Context, name string) error {
	respData, statusCode, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/admin/environments/%s", url.PathEscape(name)), nil)
	if err != nil {
		return err
	}

	if statusCode == http.StatusOK {
		return nil
	}
	if statusCode == http.StatusNotFound {
		return ErrUnleashNotFound
	}

	return fmt.Errorf("failed to get unleash environment %s (status %d): %s", name, statusCode, string(respData))
}

// DeleteEnvironment deletes an environment from Unleash (idempotent).
func (c *UnleashClient) DeleteEnvironment(ctx context.Context, name string) error {
	respData, statusCode, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/admin/environments/%s", url.PathEscape(name)), nil)
	if err != nil {
		return err
	}

	if statusCode == http.StatusOK || statusCode == http.StatusNoContent || statusCode == http.StatusNotFound {
		return nil
	}

	return fmt.Errorf("failed to delete unleash environment %s (status %d): %s", name, statusCode, string(respData))
}

// AddStrategyConstraint adds a strategy constraint for a feature in an environment.
func (c *UnleashClient) AddStrategyConstraint(ctx context.Context, projectID, featureName, envName string, constraintValues map[string]string) error {
	// constraintValues isn't really needed since the requirements hardcode it, but we can accept it or hardcode.
	// We'll use the hardcoded values as specified:
	// contextName: "divergeEnvironment", operator: "IN", values: [envName]

	payload := map[string]interface{}{
		"name": "default",
		"constraints": []map[string]interface{}{
			{
				"contextName": "divergeEnvironment",
				"operator":    "IN",
				"values":      []string{envName},
			},
		},
	}

	path := fmt.Sprintf("/api/admin/projects/%s/features/%s/environments/%s/strategies",
		url.PathEscape(projectID),
		url.PathEscape(featureName),
		url.PathEscape(envName),
	)

	respData, statusCode, err := c.doRequest(ctx, http.MethodPost, path, payload)
	if err != nil {
		return err
	}

	if statusCode == http.StatusOK || statusCode == http.StatusCreated {
		return nil
	}

	// Sometimes it might already exist and returns conflict, handle if needed, usually we can just create
	if statusCode == http.StatusConflict {
		return nil
	}

	return fmt.Errorf("failed to add strategy constraint for %s in %s (status %d): %s", featureName, envName, statusCode, string(respData))
}

// EnableFeature enables or disables a feature toggle in a specific environment.
func (c *UnleashClient) EnableFeature(ctx context.Context, projectID, featureName, envName string, enabled bool) error {
	action := "off"
	if enabled {
		action = "on"
	}

	path := fmt.Sprintf("/api/admin/projects/%s/features/%s/environments/%s/%s",
		url.PathEscape(projectID),
		url.PathEscape(featureName),
		url.PathEscape(envName),
		action,
	)

	respData, statusCode, err := c.doRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return err
	}

	if statusCode == http.StatusOK || statusCode == http.StatusNoContent || statusCode == http.StatusCreated {
		return nil
	}

	return fmt.Errorf("failed to set unleash feature %s to %s in env %s (status %d): %s", featureName, action, envName, statusCode, string(respData))
}
