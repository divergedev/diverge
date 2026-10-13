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
	"sort"
	"strings"
	"time"
)

var (
	// ErrUnleashNotFound is returned when a requested Unleash resource does not exist (HTTP 404).
	ErrUnleashNotFound = errors.New("unleash resource not found")

	// ErrUnleashUnauthorized is returned when Unleash rejects authentication (HTTP 401 or 403).
	ErrUnleashUnauthorized = errors.New("unleash authentication failed: unauthorized")
)

// UnleashConstraint represents a context constraint on an Unleash activation strategy.
type UnleashConstraint struct {
	ContextName     string   `json:"contextName"`
	Operator        string   `json:"operator"`
	Values          []string `json:"values"`
	CaseInsensitive bool     `json:"caseInsensitive,omitempty"`
	Inverted        bool     `json:"inverted,omitempty"`
}

// UnleashVariantPayload represents the payload returned when an Unleash strategy variant matches.
type UnleashVariantPayload struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// UnleashVariant represents a strategy-level variant in Unleash.
type UnleashVariant struct {
	Name       string                 `json:"name"`
	Weight     int                    `json:"weight"`
	WeightType string                 `json:"weightType"`
	Stickiness string                 `json:"stickiness"`
	Payload    *UnleashVariantPayload `json:"payload,omitempty"`
}

// UnleashStrategy represents an activation strategy attached to an Unleash feature in an environment.
type UnleashStrategy struct {
	ID          string              `json:"id,omitempty"`
	Name        string              `json:"name"`
	Title       string              `json:"title,omitempty"`
	Disabled    bool                `json:"disabled"`
	Constraints []UnleashConstraint `json:"constraints,omitempty"`
	Parameters  map[string]string   `json:"parameters,omitempty"`
	Variants    []UnleashVariant    `json:"variants,omitempty"`
}

// UnleashFeature represents feature flag metadata in Unleash.
type UnleashFeature struct {
	Name           string `json:"name"`
	Type           string `json:"type,omitempty"`
	Description    string `json:"description,omitempty"`
	ImpressionData bool   `json:"impressionData,omitempty"`
}

// UnleashClient provides an HTTP client for the Unleash Admin REST API.
type UnleashClient struct {
	baseURL     string
	projectID   string
	environment string
	adminToken  string
	clientToken string
	httpClient  *http.Client
}

// NewUnleashClient creates a new UnleashClient with validated URL and SSRF protections.
func NewUnleashClient(baseURL, projectID, environment, adminToken, clientToken string, customHTTPClient *http.Client) (*UnleashClient, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return nil, errors.New("unleash url cannot be empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid unleash url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid unleash url scheme %q (must be http or https)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, errors.New("invalid unleash url: host cannot be empty")
	}

	hostOnly := parsed.Hostname()
	lowerHost := strings.ToLower(hostOnly)
	if lowerHost == "metadata.google.internal" || lowerHost == "metadata" || lowerHost == "instance-data" {
		return nil, fmt.Errorf("prohibited unleash host %q", hostOnly)
	}
	if ip := net.ParseIP(hostOnly); ip != nil {
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.Equal(net.ParseIP("169.254.169.254")) {
			return nil, fmt.Errorf("prohibited unleash IP destination %s", hostOnly)
		}
	}

	proj := strings.TrimSpace(projectID)
	if proj == "" {
		proj = "default"
	}
	envName := strings.TrimSpace(environment)
	if envName == "" {
		envName = "development"
	}

	client := customHTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	// Protect against credential leakage on cross-origin redirects
	clientCopy := *client
	origCheckRedirect := client.CheckRedirect
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 {
			initial := via[0]
			if req.URL.Scheme != initial.URL.Scheme || req.URL.Host != initial.URL.Host {
				req.Header.Del("Authorization")
			}
		}
		if origCheckRedirect != nil {
			return origCheckRedirect(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	client = &clientCopy

	return &UnleashClient{
		baseURL:     trimmed,
		projectID:   proj,
		environment: envName,
		adminToken:  strings.TrimSpace(adminToken),
		clientToken: strings.TrimSpace(clientToken),
		httpClient:  client,
	}, nil
}

// resolvePath normalizes the endpoint subpath and avoids duplicating /api prefixes when baseURL ends in /api.
func (c *UnleashClient) resolvePath(subpath string) string {
	cleanSub := "/" + strings.TrimLeft(subpath, "/")
	if strings.HasSuffix(c.baseURL, "/api") && strings.HasPrefix(cleanSub, "/api/") {
		return strings.TrimPrefix(cleanSub, "/api")
	}
	return cleanSub
}

// doRequest performs an HTTP request against the Unleash API with configured authentication and headers.
func (c *UnleashClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, int, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshalling unleash request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	resolvedPath := c.resolvePath(path)
	fullURL := fmt.Sprintf("%s%s", c.baseURL, resolvedPath)
	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("creating unleash http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	token := c.adminToken
	if token == "" {
		token = c.clientToken
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("unleash request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading unleash response body: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return respData, resp.StatusCode, ErrUnleashNotFound
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return respData, resp.StatusCode, ErrUnleashUnauthorized
	}
	if resp.StatusCode >= 400 {
		return respData, resp.StatusCode, fmt.Errorf("unleash returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respData)))
	}

	return respData, resp.StatusCode, nil
}

// EnsureFeature ensures a feature toggle exists in the configured Unleash project.
func (c *UnleashClient) EnsureFeature(ctx context.Context, featureName string) (*UnleashFeature, error) {
	if strings.TrimSpace(featureName) == "" {
		return nil, errors.New("featureName cannot be empty")
	}

	getPath := fmt.Sprintf("/api/admin/projects/%s/features/%s", url.PathEscape(c.projectID), url.PathEscape(featureName))
	respData, _, err := c.doRequest(ctx, http.MethodGet, getPath, nil)
	if err == nil {
		var feat UnleashFeature
		if len(respData) > 0 {
			if unmarshalErr := json.Unmarshal(respData, &feat); unmarshalErr != nil {
				return nil, fmt.Errorf("unmarshalling unleash feature response: %w", unmarshalErr)
			}
		}
		if feat.Name == "" {
			feat.Name = featureName
		}
		return &feat, nil
	}
	if !errors.Is(err, ErrUnleashNotFound) {
		return nil, fmt.Errorf("getting unleash feature %q: %w", featureName, err)
	}

	createPayload := UnleashFeature{
		Name:        featureName,
		Type:        "release",
		Description: "Managed by Diverge preview environment",
	}
	createPath := fmt.Sprintf("/api/admin/projects/%s/features", url.PathEscape(c.projectID))
	createdData, _, createErr := c.doRequest(ctx, http.MethodPost, createPath, createPayload)
	if createErr != nil {
		return nil, fmt.Errorf("creating unleash feature %q: %w", featureName, createErr)
	}

	var created UnleashFeature
	if len(createdData) > 0 {
		if unmarshalErr := json.Unmarshal(createdData, &created); unmarshalErr != nil {
			return nil, fmt.Errorf("unmarshalling created unleash feature: %w", unmarshalErr)
		}
	}
	if created.Name == "" {
		created.Name = featureName
	}
	return &created, nil
}

// ListStrategies returns the strategies configured on a feature in the target Unleash environment.
func (c *UnleashClient) ListStrategies(ctx context.Context, featureName string) ([]UnleashStrategy, error) {
	if strings.TrimSpace(featureName) == "" {
		return nil, errors.New("featureName cannot be empty")
	}

	listPath := fmt.Sprintf(
		"/api/admin/projects/%s/features/%s/environments/%s/strategies",
		url.PathEscape(c.projectID),
		url.PathEscape(featureName),
		url.PathEscape(c.environment),
	)
	respData, _, err := c.doRequest(ctx, http.MethodGet, listPath, nil)
	if err != nil {
		if errors.Is(err, ErrUnleashNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing unleash strategies for %q: %w", featureName, err)
	}
	if len(respData) == 0 {
		return nil, nil
	}

	var strategies []UnleashStrategy
	if err := json.Unmarshal(respData, &strategies); err != nil {
		var wrapper struct {
			Strategies []UnleashStrategy `json:"strategies"`
		}
		if wrapErr := json.Unmarshal(respData, &wrapper); wrapErr != nil {
			return nil, fmt.Errorf("unmarshalling unleash strategies for %q: %w", featureName, err)
		}
		strategies = wrapper.Strategies
	}
	return strategies, nil
}

// UpsertPreviewStrategy creates or updates an activation strategy constrained to diverge.environment IN [previewEnvValue].
func (c *UnleashClient) UpsertPreviewStrategy(
	ctx context.Context,
	featureName, strategyTitle, previewEnvValue string,
	enabled bool,
	variantValue string,
) (*UnleashStrategy, error) {
	if strings.TrimSpace(featureName) == "" {
		return nil, errors.New("featureName cannot be empty")
	}
	if strings.TrimSpace(strategyTitle) == "" {
		return nil, errors.New("strategyTitle cannot be empty")
	}
	if strings.TrimSpace(previewEnvValue) == "" {
		return nil, errors.New("previewEnvValue cannot be empty")
	}

	rollout := "100"
	if !enabled {
		rollout = "0"
	}

	strategy := UnleashStrategy{
		Name:     "flexibleRollout",
		Title:    strategyTitle,
		Disabled: !enabled,
		Constraints: []UnleashConstraint{
			{
				ContextName: "diverge.environment",
				Operator:    "IN",
				Values:      []string{previewEnvValue},
			},
		},
		Parameters: map[string]string{
			"rollout":    rollout,
			"stickiness": "default",
			"groupId":    featureName,
		},
	}

	if variantValue != "" {
		strategy.Variants = []UnleashVariant{
			{
				Name:       variantValue,
				Weight:     1000,
				WeightType: "fix",
				Stickiness: "default",
				Payload: &UnleashVariantPayload{
					Type:  "string",
					Value: variantValue,
				},
			},
		}
	}

	existingList, err := c.ListStrategies(ctx, featureName)
	if err != nil {
		return nil, err
	}

	var existingID string
	for _, s := range existingList {
		if s.Title == strategyTitle && s.ID != "" {
			existingID = s.ID
			break
		}
	}

	basePath := fmt.Sprintf(
		"/api/admin/projects/%s/features/%s/environments/%s/strategies",
		url.PathEscape(c.projectID),
		url.PathEscape(featureName),
		url.PathEscape(c.environment),
	)

	var respData []byte
	if existingID != "" {
		strategy.ID = existingID
		putPath := fmt.Sprintf("%s/%s", basePath, url.PathEscape(existingID))
		respData, _, err = c.doRequest(ctx, http.MethodPut, putPath, strategy)
		if err != nil {
			return nil, fmt.Errorf("updating unleash strategy %q on %q: %w", existingID, featureName, err)
		}
	} else {
		respData, _, err = c.doRequest(ctx, http.MethodPost, basePath, strategy)
		if err != nil {
			return nil, fmt.Errorf("creating unleash strategy on %q: %w", featureName, err)
		}
	}

	var saved UnleashStrategy
	if len(respData) > 0 {
		if unmarshalErr := json.Unmarshal(respData, &saved); unmarshalErr != nil {
			return nil, fmt.Errorf("unmarshalling unleash strategy response: %w", unmarshalErr)
		}
	}
	if saved.Title == "" {
		saved.Title = strategyTitle
	}
	return &saved, nil
}

// ListProjectFeatures lists feature names in the configured Unleash project.
func (c *UnleashClient) ListProjectFeatures(ctx context.Context) ([]string, error) {
	path := fmt.Sprintf("/api/admin/projects/%s/features", url.PathEscape(c.projectID))
	respData, _, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		if errors.Is(err, ErrUnleashNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing unleash project features: %w", err)
	}
	if len(respData) == 0 {
		return nil, nil
	}

	var wrapper struct {
		Features []UnleashFeature `json:"features"`
	}
	if err := json.Unmarshal(respData, &wrapper); err != nil {
		var list []UnleashFeature
		if arrErr := json.Unmarshal(respData, &list); arrErr != nil {
			return nil, fmt.Errorf("unmarshalling unleash features list: %w", err)
		}
		wrapper.Features = list
	}

	names := make([]string, 0, len(wrapper.Features))
	for _, f := range wrapper.Features {
		if f.Name != "" {
			names = append(names, f.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// DeletePreviewStrategies deletes all strategies matching strategyTitle across the specified features (or all project features when empty).
func (c *UnleashClient) DeletePreviewStrategies(ctx context.Context, strategyTitle string, featureNames []string) error {
	if strings.TrimSpace(strategyTitle) == "" {
		return errors.New("strategyTitle cannot be empty")
	}

	targets := featureNames
	if len(targets) == 0 {
		discovered, err := c.ListProjectFeatures(ctx)
		if err != nil {
			return err
		}
		targets = discovered
	}

	for _, featureName := range targets {
		strategies, err := c.ListStrategies(ctx, featureName)
		if err != nil {
			return err
		}
		for _, s := range strategies {
			if s.Title != strategyTitle || s.ID == "" {
				continue
			}
			delPath := fmt.Sprintf(
				"/api/admin/projects/%s/features/%s/environments/%s/strategies/%s",
				url.PathEscape(c.projectID),
				url.PathEscape(featureName),
				url.PathEscape(c.environment),
				url.PathEscape(s.ID),
			)
			if _, _, delErr := c.doRequest(ctx, http.MethodDelete, delPath, nil); delErr != nil && !errors.Is(delErr, ErrUnleashNotFound) {
				return fmt.Errorf("deleting unleash strategy %q on feature %q: %w", s.ID, featureName, delErr)
			}
		}
	}
	return nil
}

// HealthCheck verifies connectivity and authorization to the Unleash instance.
func (c *UnleashClient) HealthCheck(ctx context.Context) error {
	_, statusCode, err := c.doRequest(ctx, http.MethodGet, "/internal-backstage/health", nil)
	if err != nil {
		if errors.Is(err, ErrUnleashNotFound) || statusCode == http.StatusNotFound {
			projPath := fmt.Sprintf("/api/admin/projects/%s", url.PathEscape(c.projectID))
			if _, _, projErr := c.doRequest(ctx, http.MethodGet, projPath, nil); projErr != nil {
				return fmt.Errorf("unleash health check failed: %w", projErr)
			}
			return nil
		}
		return fmt.Errorf("unleash health check failed: %w", err)
	}
	return nil
}
