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
	"syscall"
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
	SortOrder   int                 `json:"sortOrder"`
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

func isProhibitedUnleashIP(ip net.IP) bool {
	return ip == nil || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.Equal(net.ParseIP("100.100.100.200"))
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
		if isProhibitedUnleashIP(ip) {
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
		dialer := &net.Dialer{
			Timeout: 5 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				host, _, splitErr := net.SplitHostPort(address)
				if splitErr != nil {
					return splitErr
				}
				ip := net.ParseIP(host)
				if isProhibitedUnleashIP(ip) {
					return fmt.Errorf("prohibited unleash IP destination %s", host)
				}
				return nil
			},
		}
		client = &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{DialContext: dialer.DialContext},
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

// EnableFeatureInEnvironment enables a feature toggle in the configured Unleash environment.
func (c *UnleashClient) EnableFeatureInEnvironment(ctx context.Context, featureName string) error {
	if strings.TrimSpace(featureName) == "" {
		return errors.New("featureName cannot be empty")
	}

	enablePath := fmt.Sprintf(
		"/api/admin/projects/%s/features/%s/environments/%s/on",
		url.PathEscape(c.projectID),
		url.PathEscape(featureName),
		url.PathEscape(c.environment),
	)
	if _, _, err := c.doRequest(ctx, http.MethodPost, enablePath, nil); err != nil {
		return fmt.Errorf("enabling unleash feature %q in environment %q: %w", featureName, c.environment, err)
	}
	return nil
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
// When enabled is false, it also adds a diverge.environment NOT_IN [previewEnvValue] exclusion constraint to any other
// strategies on the feature so broader strategies cannot turn the flag on in the preview environment.
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
		Name:      "flexibleRollout",
		Title:     strategyTitle,
		Disabled:  !enabled,
		SortOrder: 0,
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

	if err := c.reconcileOtherStrategiesForPreview(ctx, featureName, basePath, strategyTitle, saved.ID, previewEnvValue, enabled, existingList); err != nil {
		return nil, err
	}

	return &saved, nil
}

// reconcileOtherStrategiesForPreview ensures that:
// 1. When enabled == false, non-preview strategies exclude previewEnvValue via a diverge.environment NOT_IN constraint.
// 2. When enabled == true, any prior exclusion for previewEnvValue is removed from non-preview strategies.
// 3. The preview strategy is ordered first so its variants take precedence over broader strategies.
func (c *UnleashClient) reconcileOtherStrategiesForPreview(
	ctx context.Context,
	featureName, basePath, strategyTitle, previewStrategyID, previewEnvValue string,
	enabled bool,
	existingList []UnleashStrategy,
) error {
	for _, other := range existingList {
		if other.Title == strategyTitle || other.ID == "" {
			continue
		}
		var updated UnleashStrategy
		var changed bool
		if !enabled {
			updated, changed = addPreviewExclusionConstraint(other, previewEnvValue)
		} else {
			updated, changed = removePreviewExclusionConstraint(other, previewEnvValue)
		}
		if changed {
			putPath := fmt.Sprintf("%s/%s", basePath, url.PathEscape(other.ID))
			if _, _, err := c.doRequest(ctx, http.MethodPut, putPath, updated); err != nil {
				return fmt.Errorf("updating exclusion constraint on strategy %q for %q: %w", other.ID, featureName, err)
			}
		}
	}

	// Ensure preview strategy is evaluated first when other strategies exist
	if previewStrategyID != "" && len(existingList) > 0 && existingList[0].ID != previewStrategyID {
		type sortOrderEntry struct {
			ID        string `json:"id"`
			SortOrder int    `json:"sortOrder"`
		}
		orderPayload := []sortOrderEntry{{ID: previewStrategyID, SortOrder: 0}}
		orderIdx := 1
		for _, s := range existingList {
			if s.ID == "" || s.ID == previewStrategyID || s.Title == strategyTitle {
				continue
			}
			orderPayload = append(orderPayload, sortOrderEntry{ID: s.ID, SortOrder: orderIdx})
			orderIdx++
		}
		if len(orderPayload) > 1 {
			sortPath := fmt.Sprintf("%s/set-strategies-sort-order", basePath)
			if _, _, err := c.doRequest(ctx, http.MethodPut, sortPath, orderPayload); err != nil && !errors.Is(err, ErrUnleashNotFound) {
				return fmt.Errorf("setting unleash strategy sort order on %q: %w", featureName, err)
			}
		}
	}

	return nil
}

func addPreviewExclusionConstraint(s UnleashStrategy, previewEnvValue string) (UnleashStrategy, bool) {
	constraints := make([]UnleashConstraint, len(s.Constraints))
	copy(constraints, s.Constraints)

	for i := range constraints {
		c := &constraints[i]
		if c.ContextName == "diverge.environment" && (c.Operator == "NOT_IN" || (c.Operator == "IN" && c.Inverted)) {
			for _, v := range c.Values {
				if v == previewEnvValue {
					return s, false
				}
			}
			vals := make([]string, len(c.Values)+1)
			copy(vals, c.Values)
			vals[len(c.Values)] = previewEnvValue
			c.Values = vals
			s.Constraints = constraints
			return s, true
		}
	}

	constraints = append(constraints, UnleashConstraint{
		ContextName: "diverge.environment",
		Operator:    "NOT_IN",
		Values:      []string{previewEnvValue},
	})
	s.Constraints = constraints
	return s, true
}

func removePreviewExclusionConstraint(s UnleashStrategy, previewEnvValue string) (UnleashStrategy, bool) {
	if len(s.Constraints) == 0 || previewEnvValue == "" {
		return s, false
	}
	var updated []UnleashConstraint
	changed := false
	for _, c := range s.Constraints {
		if c.ContextName == "diverge.environment" && (c.Operator == "NOT_IN" || (c.Operator == "IN" && c.Inverted)) {
			var remaining []string
			for _, v := range c.Values {
				if v == previewEnvValue {
					changed = true
					continue
				}
				remaining = append(remaining, v)
			}
			if len(remaining) > 0 {
				c.Values = remaining
				updated = append(updated, c)
			}
			continue
		}
		updated = append(updated, c)
	}
	if !changed {
		return s, false
	}
	s.Constraints = updated
	return s, true
}

func extractPreviewEnvValueFromStrategy(s UnleashStrategy) string {
	for _, c := range s.Constraints {
		if c.ContextName == "diverge.environment" && c.Operator == "IN" && !c.Inverted && len(c.Values) > 0 {
			return c.Values[0]
		}
	}
	return ""
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

// DeletePreviewStrategies deletes all strategies matching strategyTitle across the specified features (or all project features when empty)
// and cleans up any diverge.environment NOT_IN exclusion constraints added for that preview environment.
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

		basePath := fmt.Sprintf(
			"/api/admin/projects/%s/features/%s/environments/%s/strategies",
			url.PathEscape(c.projectID),
			url.PathEscape(featureName),
			url.PathEscape(c.environment),
		)

		var previewEnvVal string
		for _, s := range strategies {
			if s.Title != strategyTitle || s.ID == "" {
				continue
			}
			if previewEnvVal == "" {
				previewEnvVal = extractPreviewEnvValueFromStrategy(s)
			}
			delPath := fmt.Sprintf("%s/%s", basePath, url.PathEscape(s.ID))
			if _, _, delErr := c.doRequest(ctx, http.MethodDelete, delPath, nil); delErr != nil && !errors.Is(delErr, ErrUnleashNotFound) {
				return fmt.Errorf("deleting unleash strategy %q on feature %q: %w", s.ID, featureName, delErr)
			}
		}

		if previewEnvVal != "" {
			for _, other := range strategies {
				if other.Title == strategyTitle || other.ID == "" {
					continue
				}
				if updated, changed := removePreviewExclusionConstraint(other, previewEnvVal); changed {
					putPath := fmt.Sprintf("%s/%s", basePath, url.PathEscape(other.ID))
					if _, _, putErr := c.doRequest(ctx, http.MethodPut, putPath, updated); putErr != nil && !errors.Is(putErr, ErrUnleashNotFound) {
						return fmt.Errorf("cleaning exclusion constraint on strategy %q for feature %q: %w", other.ID, featureName, putErr)
					}
				}
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
