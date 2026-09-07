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

// ErrFliptNotFound is returned when a requested Flipt resource is not found (HTTP 404).
var ErrFliptNotFound = errors.New("flipt resource not found")

// FliptClient provides a lightweight HTTP client for interacting with Flipt's v1 REST API.
type FliptClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewFliptClient creates a new FliptClient with a bounded HTTP timeout.
func NewFliptClient(baseURL, token string, customHTTPClient *http.Client) (*FliptClient, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid flipt url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid flipt url scheme %q (must be http or https)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("invalid flipt url host cannot be empty")
	}

	client := customHTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	return &FliptClient{
		baseURL:    trimmed,
		token:      strings.TrimSpace(token),
		httpClient: client,
	}, nil
}

func (c *FliptClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, int, error) {
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
		return nil, 0, fmt.Errorf("flipt request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response body: %w", err)
	}

	return respData, resp.StatusCode, nil
}

// CreateNamespace idempotently creates a namespace in Flipt.
func (c *FliptClient) CreateNamespace(ctx context.Context, key, name, description string) error {
	payload := map[string]string{
		"key":         key,
		"name":        name,
		"description": description,
	}

	respData, statusCode, err := c.doRequest(ctx, http.MethodPost, "/api/v1/namespaces", payload)
	if err != nil {
		return err
	}

	if statusCode == http.StatusOK || statusCode == http.StatusCreated {
		return nil
	}
	// If already exists (409 Conflict), treat as success (idempotent)
	if statusCode == http.StatusConflict {
		return nil
	}

	return fmt.Errorf("failed to create flipt namespace %s (status %d): %s", key, statusCode, string(respData))
}

// GetNamespace retrieves a namespace by key from Flipt.
func (c *FliptClient) GetNamespace(ctx context.Context, key string) error {
	respData, statusCode, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/namespaces/%s", url.PathEscape(key)), nil)
	if err != nil {
		return err
	}

	if statusCode == http.StatusOK {
		return nil
	}
	if statusCode == http.StatusNotFound {
		return ErrFliptNotFound
	}

	return fmt.Errorf("failed to get flipt namespace %s (status %d): %s", key, statusCode, string(respData))
}

// DeleteNamespace deletes a namespace from Flipt (idempotent; 404 is treated as success).
func (c *FliptClient) DeleteNamespace(ctx context.Context, key string) error {
	respData, statusCode, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/namespaces/%s", url.PathEscape(key)), nil)
	if err != nil {
		return err
	}

	if statusCode == http.StatusOK || statusCode == http.StatusNoContent || statusCode == http.StatusNotFound {
		return nil
	}

	return fmt.Errorf("failed to delete flipt namespace %s (status %d): %s", key, statusCode, string(respData))
}

// CreateOrUpdateFlag creates or updates a boolean or variant flag in a given Flipt namespace.
func (c *FliptClient) CreateOrUpdateFlag(ctx context.Context, namespaceKey, flagKey, flagType string, enabled bool, variantVal string) error {
	payload := map[string]interface{}{
		"key":         flagKey,
		"name":        flagKey,
		"type":        flagType,
		"enabled":     enabled,
		"description": "Managed by Diverge preview environment",
	}

	flagsPath := fmt.Sprintf("/api/v1/namespaces/%s/flags", url.PathEscape(namespaceKey))
	respData, statusCode, err := c.doRequest(ctx, http.MethodPost, flagsPath, payload)
	if err != nil {
		return err
	}

	// If already exists, update the flag
	if statusCode == http.StatusConflict {
		flagDetailPath := fmt.Sprintf("%s/%s", flagsPath, url.PathEscape(flagKey))
		respData, statusCode, err = c.doRequest(ctx, http.MethodPut, flagDetailPath, payload)
		if err != nil {
			return err
		}
	}

	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		return fmt.Errorf("failed to set flag %s in flipt namespace %s (status %d): %s", flagKey, namespaceKey, statusCode, string(respData))
	}

	// For variant flags, register the variant value
	if flagType == "VARIANT_FLAG_TYPE" && variantVal != "" {
		variantPayload := map[string]string{
			"key":         "value",
			"name":        variantVal,
			"description": "Diverge preview variant value",
		}
		variantPath := fmt.Sprintf("%s/%s/variants", flagsPath, url.PathEscape(flagKey))
		varRespData, varStatus, varErr := c.doRequest(ctx, http.MethodPost, variantPath, variantPayload)
		if varErr != nil {
			return varErr
		}
		if varStatus == http.StatusConflict {
			// Already exists; update variant
			variantPutPath := fmt.Sprintf("%s/value", variantPath)
			varRespData, varStatus, varErr = c.doRequest(ctx, http.MethodPut, variantPutPath, variantPayload)
			if varErr != nil {
				return varErr
			}
		}
		if varStatus != http.StatusOK && varStatus != http.StatusCreated {
			return fmt.Errorf("failed to set variant for flag %s in flipt (status %d): %s", flagKey, varStatus, string(varRespData))
		}
	}

	return nil
}
