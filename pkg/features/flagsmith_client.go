package features

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrFlagsmithNotFound is returned when a requested Flagsmith resource does not exist (HTTP 404).
	ErrFlagsmithNotFound = errors.New("flagsmith resource not found")

	// ErrFlagsmithUnauthorized is returned when Flagsmith rejects authentication (HTTP 401 or 403).
	ErrFlagsmithUnauthorized = errors.New("flagsmith authentication failed: unauthorized")
)

// FlagsmithTrait represents a trait key-value pair associated with an identity.
type FlagsmithTrait struct {
	TraitKey   string      `json:"trait_key"`
	TraitValue interface{} `json:"trait_value"`
}

// FlagsmithFeature represents feature flag metadata in Flagsmith.
type FlagsmithFeature struct {
	ID   int64  `json:"id,omitempty"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// FlagsmithFeatureState represents the evaluated or overridden state of a flag.
type FlagsmithFeatureState struct {
	ID                int64            `json:"id,omitempty"`
	Feature           FlagsmithFeature `json:"feature"`
	Enabled           bool             `json:"enabled"`
	FeatureStateValue interface{}      `json:"feature_state_value,omitempty"`
}

// FlagsmithIdentity represents an identified user or preview session with traits and flag overrides.
type FlagsmithIdentity struct {
	ID         int64                   `json:"id,omitempty"`
	Identifier string                  `json:"identifier"`
	Traits     []FlagsmithTrait        `json:"traits,omitempty"`
	Flags      []FlagsmithFeatureState `json:"flags,omitempty"`
}

// FlagsmithClient provides an HTTP client for Flagsmith's Edge and Core REST API.
type FlagsmithClient struct {
	baseURL        string
	environmentKey string
	masterAPIKey   string
	httpClient     *http.Client
}

// NewFlagsmithClient creates a new FlagsmithClient with validated URL and SSRF protections.
func NewFlagsmithClient(baseURL, environmentKey, masterAPIKey string, customHTTPClient *http.Client) (*FlagsmithClient, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		trimmed = "https://edge.api.flagsmith.com/api/v1"
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid flagsmith url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid flagsmith url scheme %q (must be http or https)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("invalid flagsmith url: host cannot be empty")
	}

	hostOnly := parsed.Hostname()
	lowerHost := strings.ToLower(hostOnly)
	if lowerHost == "metadata.google.internal" || lowerHost == "metadata" || lowerHost == "instance-data" {
		return nil, fmt.Errorf("prohibited flagsmith host %q", hostOnly)
	}
	if ip := net.ParseIP(hostOnly); ip != nil {
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.Equal(net.ParseIP("169.254.169.254")) {
			return nil, fmt.Errorf("prohibited flagsmith IP destination %s", hostOnly)
		}
	}

	client := customHTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	return &FlagsmithClient{
		baseURL:        trimmed,
		environmentKey: strings.TrimSpace(environmentKey),
		masterAPIKey:   strings.TrimSpace(masterAPIKey),
		httpClient:     client,
	}, nil
}

// resolvePath normalizes the endpoint subpath and avoids duplicating /api/v1 prefixes.
func (c *FlagsmithClient) resolvePath(subpath string) string {
	cleanSub := "/" + strings.TrimLeft(subpath, "/")
	if strings.HasSuffix(c.baseURL, "/api/v1") && strings.HasPrefix(cleanSub, "/api/v1/") {
		return strings.TrimPrefix(cleanSub, "/api/v1")
	}
	return cleanSub
}

// doRequest performs an HTTP request against the Flagsmith API with configured authentication and headers.
func (c *FlagsmithClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, int, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshalling flagsmith request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	resolvedPath := c.resolvePath(path)
	fullURL := fmt.Sprintf("%s%s", c.baseURL, resolvedPath)
	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("creating flagsmith http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// SDK environment key authentication header
	if c.environmentKey != "" {
		req.Header.Set("X-Environment-Key", c.environmentKey)
	}

	// Master / Admin API key authentication header
	if c.masterAPIKey != "" {
		req.Header.Set("Authorization", "Api-Key "+c.masterAPIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("flagsmith request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading flagsmith response body: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return respData, resp.StatusCode, ErrFlagsmithNotFound
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return respData, resp.StatusCode, ErrFlagsmithUnauthorized
	}
	if resp.StatusCode >= 400 {
		return respData, resp.StatusCode, fmt.Errorf("flagsmith returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respData)))
	}

	return respData, resp.StatusCode, nil
}

// EnsureIdentityWithTraits creates or updates an identity in Flagsmith with the provided traits.
func (c *FlagsmithClient) EnsureIdentityWithTraits(ctx context.Context, identifier string, traits map[string]interface{}) (*FlagsmithIdentity, error) {
	if identifier == "" {
		return nil, errors.New("identifier cannot be empty")
	}

	traitList := make([]FlagsmithTrait, 0, len(traits))
	for k, v := range traits {
		traitList = append(traitList, FlagsmithTrait{
			TraitKey:   k,
			TraitValue: v,
		})
	}

	payload := map[string]interface{}{
		"identifier": identifier,
		"traits":     traitList,
	}

	respData, _, err := c.doRequest(ctx, http.MethodPost, "/api/v1/identities/", payload)
	if err != nil {
		return nil, fmt.Errorf("ensuring flagsmith identity %q: %w", identifier, err)
	}

	var identity FlagsmithIdentity
	if err := json.Unmarshal(respData, &identity); err != nil {
		return nil, fmt.Errorf("unmarshalling flagsmith identity response: %w", err)
	}

	if identity.Identifier == "" {
		identity.Identifier = identifier
	}
	return &identity, nil
}

// SetIdentityFeatureState creates or updates a feature state override for an identity.
func (c *FlagsmithClient) SetIdentityFeatureState(ctx context.Context, identifier, featureName string, enabled bool, value interface{}) (*FlagsmithFeatureState, error) {
	if identifier == "" {
		return nil, errors.New("identifier cannot be empty")
	}
	if featureName == "" {
		return nil, errors.New("featureName cannot be empty")
	}

	payload := map[string]interface{}{
		"feature": map[string]interface{}{
			"name": featureName,
		},
		"enabled":             enabled,
		"feature_state_value": value,
	}

	// Try environment-scoped endpoint first
	endpoint := fmt.Sprintf("/api/v1/environments/%s/identities/%s/featurestates/", c.environmentKey, identifier)
	if c.environmentKey == "" {
		endpoint = fmt.Sprintf("/api/v1/identities/%s/featurestates/", identifier)
	}

	respData, _, err := c.doRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil && errors.Is(err, ErrFlagsmithNotFound) {
		// Fall back to alternative identity featurestates route
		altEndpoint := fmt.Sprintf("/api/v1/identities/%s/featurestates/", identifier)
		var altErr error
		respData, _, altErr = c.doRequest(ctx, http.MethodPost, altEndpoint, payload)
		if altErr != nil {
			return nil, fmt.Errorf("setting flagsmith feature state for %s/%s: %w", identifier, featureName, altErr)
		}
	} else if err != nil {
		return nil, fmt.Errorf("setting flagsmith feature state for %s/%s: %w", identifier, featureName, err)
	}

	var state FlagsmithFeatureState
	if err := json.Unmarshal(respData, &state); err != nil {
		return nil, fmt.Errorf("unmarshalling flagsmith feature state response: %w", err)
	}

	if state.Feature.Name == "" {
		state.Feature.Name = featureName
	}
	return &state, nil
}

// DeleteIdentity removes an ephemeral identity and its associated overrides from Flagsmith.
func (c *FlagsmithClient) DeleteIdentity(ctx context.Context, identifier string) error {
	if identifier == "" {
		return errors.New("identifier cannot be empty")
	}

	endpoint := fmt.Sprintf("/api/v1/environments/%s/identities/%s/", c.environmentKey, identifier)
	if c.environmentKey == "" {
		endpoint = fmt.Sprintf("/api/v1/identities/%s/", identifier)
	}

	_, _, err := c.doRequest(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		if errors.Is(err, ErrFlagsmithNotFound) {
			// Already deleted or absent
			return nil
		}
		// Fallback to query parameter endpoint /api/v1/identities/?identifier={id}
		queryEndpoint := fmt.Sprintf("/api/v1/identities/?identifier=%s", url.QueryEscape(identifier))
		_, _, queryErr := c.doRequest(ctx, http.MethodDelete, queryEndpoint, nil)
		if queryErr != nil && !errors.Is(queryErr, ErrFlagsmithNotFound) {
			return fmt.Errorf("deleting flagsmith identity %q: %w", identifier, queryErr)
		}
	}
	return nil
}

// HealthCheck verifies connectivity and authorization to Flagsmith.
func (c *FlagsmithClient) HealthCheck(ctx context.Context) error {
	endpoint := "/api/v1/flags/"
	if c.environmentKey == "" && c.masterAPIKey == "" {
		endpoint = "/health"
	}

	_, statusCode, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		// Some Flagsmith instances only expose /health or root
		if errors.Is(err, ErrFlagsmithNotFound) || statusCode == http.StatusNotFound {
			_, _, healthErr := c.doRequest(ctx, http.MethodGet, "/health", nil)
			if healthErr != nil {
				return fmt.Errorf("flagsmith health check failed: %w", healthErr)
			}
			return nil
		}
		return fmt.Errorf("flagsmith health check failed: %w", err)
	}
	return nil
}
