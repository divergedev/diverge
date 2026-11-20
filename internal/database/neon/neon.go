package neon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgdb "github.com/divergedev/diverge/pkg/database"
)

const (
	// DefaultBaseURL is the production Neon REST API v2 base endpoint.
	DefaultBaseURL = "https://console.neon.tech/api/v2"
	// DefaultDatabaseName is the default database queried when resolving connection URIs.
	DefaultDatabaseName = "neondb"
	// DefaultRoleName is the default role queried when resolving connection URIs.
	DefaultRoleName = "neondb_owner"

	// AnnotationParentBranch optionally overrides the parent Neon branch ID for CoW branching.
	AnnotationParentBranch = "divergedev.com/neon-parent-branch"

	maxNeonResponseBytes = 2 << 20 // 2 MiB
	maxBranchPages       = 50
	defaultHTTPTimeout   = 15 * time.Second
)

var (
	safeNeonIDRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
	nonBranchChars  = regexp.MustCompile(`[^a-z0-9-]`)
	multiDashRegex  = regexp.MustCompile(`-{2,}`)
)

// NeonDatabaseProvider provisions and tears down instant Copy-on-Write Postgres branches
// in Neon Serverless Postgres via the Neon REST API v2.
type NeonDatabaseProvider struct {
	Client         client.Client
	APIKey         string
	ProjectID      string
	ParentBranchID string
	DatabaseName   string
	RoleName       string
	BaseURL        string

	// HTTPClient optionally overrides the HTTP client used for Neon API calls.
	HTTPClient *http.Client
	// AllowInsecureHTTPForTesting permits http:// BaseURL endpoints in unit tests.
	AllowInsecureHTTPForTesting bool
	// AllowLoopbackForTesting permits loopback/private IP dials in unit tests.
	AllowLoopbackForTesting bool
	// LookupIPAddr overrides DNS resolution in dial guards when non-nil (for testing).
	LookupIPAddr func(ctx context.Context, host string) ([]net.IPAddr, error)
}

var _ pkgdb.DatabaseProvider = (*NeonDatabaseProvider)(nil)

// ProviderName returns the canonical database provider identifier ("neon").
func (p *NeonDatabaseProvider) ProviderName() string {
	return "neon"
}

type resolvedNeonConfig struct {
	apiKey         string
	projectID      string
	parentBranchID string
	databaseName   string
	roleName       string
	baseURL        string
}

type neonBranch struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ParentID     string `json:"parent_id,omitempty"`
	CurrentState string `json:"current_state"`
}

type neonListBranchesResponse struct {
	Branches   []neonBranch `json:"branches"`
	Pagination struct {
		Next string `json:"next"`
	} `json:"pagination"`
}

type neonCreateBranchRequest struct {
	Branch    neonBranchCreateSpec   `json:"branch"`
	Endpoints []neonEndpointTypeSpec `json:"endpoints,omitempty"`
}

type neonBranchCreateSpec struct {
	Name     string `json:"name"`
	ParentID string `json:"parent_id,omitempty"`
}

type neonEndpointTypeSpec struct {
	Type string `json:"type"`
}

type neonCreateBranchResponse struct {
	Branch neonBranch `json:"branch"`
}

type neonConnectionURIResponse struct {
	URI string `json:"uri"`
}

// BranchName computes a deterministic, collision-free Neon branch name (`diverge-<env>-<8hex>`)
// scoped to both namespace and environment name (max 63 characters).
func BranchName(envNamespace, envName string) string {
	ns := strings.TrimSpace(envNamespace)
	if ns == "" {
		ns = "default"
	}
	clean := strings.ToLower(strings.TrimSpace(envName))
	clean = strings.NewReplacer(".", "-", "_", "-").Replace(clean)
	clean = nonBranchChars.ReplaceAllString(clean, "")
	clean = multiDashRegex.ReplaceAllString(clean, "-")
	clean = strings.Trim(clean, "-")
	if clean == "" {
		clean = "env"
	}

	sum := sha256.Sum256([]byte(ns + "/" + strings.TrimSpace(envName)))
	suffix := hex.EncodeToString(sum[:4]) // 8 hex chars
	prefix := "diverge-" + clean
	const maxLen = 63
	maxPrefix := maxLen - 1 - len(suffix)
	if len(prefix) > maxPrefix {
		prefix = strings.TrimRight(prefix[:maxPrefix], "-")
	}
	if prefix == "" {
		prefix = "diverge"
	}
	return prefix + "-" + suffix
}

func firstSecretVal(data map[string][]byte, keys ...string) string {
	for _, k := range keys {
		if raw, ok := data[k]; ok {
			if trimmed := strings.TrimSpace(string(raw)); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func (p *NeonDatabaseProvider) resolveConfig(ctx context.Context, env *v1alpha1.Environment) (*resolvedNeonConfig, error) {
	cfg := &resolvedNeonConfig{
		apiKey:         strings.TrimSpace(p.APIKey),
		projectID:      strings.TrimSpace(p.ProjectID),
		parentBranchID: strings.TrimSpace(p.ParentBranchID),
		databaseName:   strings.TrimSpace(p.DatabaseName),
		roleName:       strings.TrimSpace(p.RoleName),
		baseURL:        strings.TrimSpace(p.BaseURL),
	}

	if env != nil {
		if secretName := strings.TrimSpace(env.Spec.Database.ConnectionRef); secretName != "" {
			if p.Client == nil {
				return nil, fmt.Errorf("kubernetes client is required to read Neon connectionRef secret %q", secretName)
			}
			ns := strings.TrimSpace(env.Namespace)
			if ns == "" {
				ns = "default"
			}
			var sec corev1.Secret
			if err := p.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: secretName}, &sec); err != nil {
				return nil, fmt.Errorf("fetching Neon connectionRef secret %s/%s: %w", ns, secretName, err)
			}
			secretAPIKey := firstSecretVal(sec.Data, "apiKey", "api-key", "NEON_API_KEY")
			secretBaseURL := firstSecretVal(sec.Data, "baseUrl", "base-url", "NEON_API_BASE_URL")
			if secretBaseURL != "" && secretAPIKey == "" && cfg.apiKey != "" {
				controllerBase := cfg.baseURL
				if controllerBase == "" {
					controllerBase = DefaultBaseURL
				}
				if !strings.EqualFold(strings.TrimRight(secretBaseURL, "/"), strings.TrimRight(controllerBase, "/")) {
					return nil, errors.New("connectionRef secret overriding Neon baseUrl must also provide its own apiKey")
				}
			}
			if secretAPIKey != "" {
				cfg.apiKey = secretAPIKey
			}
			if v := firstSecretVal(sec.Data, "projectId", "project-id", "NEON_PROJECT_ID"); v != "" {
				cfg.projectID = v
			}
			if v := firstSecretVal(sec.Data, "parentBranchId", "parent-branch-id", "NEON_PARENT_BRANCH_ID"); v != "" {
				cfg.parentBranchID = v
			}
			if v := firstSecretVal(sec.Data, "databaseName", "database-name", "NEON_DATABASE_NAME"); v != "" {
				cfg.databaseName = v
			}
			if v := firstSecretVal(sec.Data, "roleName", "role-name", "NEON_ROLE_NAME"); v != "" {
				cfg.roleName = v
			}
			if secretBaseURL != "" {
				cfg.baseURL = secretBaseURL
			}
		}

		if seed := strings.TrimSpace(env.Spec.Database.SeedSource); strings.HasPrefix(seed, "branch:") {
			if branchID := strings.TrimSpace(strings.TrimPrefix(seed, "branch:")); branchID != "" {
				cfg.parentBranchID = branchID
			}
		}
		if annParent := strings.TrimSpace(env.Annotations[AnnotationParentBranch]); annParent != "" {
			cfg.parentBranchID = annParent
		}
	}

	if cfg.databaseName == "" {
		cfg.databaseName = DefaultDatabaseName
	}
	if cfg.roleName == "" {
		cfg.roleName = DefaultRoleName
	}
	if cfg.baseURL == "" {
		cfg.baseURL = DefaultBaseURL
	}
	cfg.baseURL = strings.TrimRight(cfg.baseURL, "/")

	parsedURL, err := url.Parse(cfg.baseURL)
	if err != nil || parsedURL.Host == "" {
		return nil, fmt.Errorf("invalid Neon API base URL %q", cfg.baseURL)
	}
	if parsedURL.User != nil {
		return nil, errors.New("neon API base URL must not embed user credentials")
	}
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "https" && (scheme != "http" || !p.AllowInsecureHTTPForTesting) {
		return nil, fmt.Errorf("neon API base URL must use https:// (got %q)", parsedURL.Scheme)
	}

	if cfg.apiKey == "" {
		return nil, errors.New("neon API key is not configured (set connectionRef secret key 'apiKey' or DIVERGE_NEON_API_KEY)")
	}
	if cfg.projectID == "" {
		return nil, errors.New("neon project ID is not configured (set connectionRef secret key 'projectId' or DIVERGE_NEON_PROJECT_ID)")
	}
	if !safeNeonIDRegex.MatchString(cfg.projectID) {
		return nil, fmt.Errorf("invalid Neon project ID %q", cfg.projectID)
	}
	if cfg.parentBranchID != "" && !safeNeonIDRegex.MatchString(cfg.parentBranchID) {
		return nil, fmt.Errorf("invalid Neon parent branch ID %q", cfg.parentBranchID)
	}

	return cfg, nil
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

func (p *NeonDatabaseProvider) httpClient() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	lookup := p.LookupIPAddr
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	allowLoopback := p.AllowLoopbackForTesting

	transport := &http.Transport{
		Proxy:               nil, // Prevent HTTP_PROXY / HTTPS_PROXY SSRF bypass
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     60 * time.Second,
		MaxIdleConns:        32,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid dial address %q: %w", addr, err)
			}
			addrs, err := lookup(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("resolving Neon API host %q: %w", host, err)
			}
			if len(addrs) == 0 {
				return nil, fmt.Errorf("no IP addresses resolved for Neon API host %q", host)
			}
			for _, ipAddr := range addrs {
				if !allowLoopback && isBlockedIP(ipAddr.IP) {
					return nil, fmt.Errorf("neon API host %q resolved to blocked non-public IP %s", host, ipAddr.IP.String())
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(addrs[0].IP.String(), port))
		},
	}

	return &http.Client{
		Timeout:   defaultHTTPTimeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (p *NeonDatabaseProvider) doJSON(ctx context.Context, cfg *resolvedNeonConfig, method, endpoint string, reqBody any, respOut any) (int, []byte, error) {
	var bodyReader io.Reader
	if reqBody != nil {
		encoded, err := json.Marshal(reqBody)
		if err != nil {
			return 0, nil, fmt.Errorf("encoding Neon API request: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("constructing Neon API request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.apiKey)
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("calling Neon API %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxNeonResponseBytes))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("reading Neon API response: %w", err)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 && respOut != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, respOut); err != nil {
			return resp.StatusCode, raw, fmt.Errorf("decoding Neon API response: %w", err)
		}
	}
	return resp.StatusCode, raw, nil
}

func extractNeonAPIError(status int, raw []byte) string {
	var parsed struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &parsed) == nil && strings.TrimSpace(parsed.Message) != "" {
		if parsed.Code != "" {
			return fmt.Sprintf("HTTP %d (%s): %s", status, parsed.Code, strings.TrimSpace(parsed.Message))
		}
		return fmt.Sprintf("HTTP %d: %s", status, strings.TrimSpace(parsed.Message))
	}
	return fmt.Sprintf("HTTP %d", status)
}

func (p *NeonDatabaseProvider) findBranchByName(ctx context.Context, cfg *resolvedNeonConfig, branchName string) (*neonBranch, error) {
	cursor := ""
	for page := 0; page < maxBranchPages; page++ {
		u := fmt.Sprintf("%s/projects/%s/branches?limit=100", cfg.baseURL, url.PathEscape(cfg.projectID))
		if cursor != "" {
			u += "&cursor=" + url.QueryEscape(cursor)
		}
		var listResp neonListBranchesResponse
		status, raw, err := p.doJSON(ctx, cfg, http.MethodGet, u, nil, &listResp)
		if err != nil {
			return nil, err
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("neon list branches failed (%s)", extractNeonAPIError(status, raw))
		}
		for i := range listResp.Branches {
			if listResp.Branches[i].Name == branchName {
				return &listResp.Branches[i], nil
			}
		}
		next := strings.TrimSpace(listResp.Pagination.Next)
		if next == "" || next == cursor {
			return nil, nil
		}
		cursor = next
	}
	return nil, fmt.Errorf("neon branch listing exceeded %d pages without exhausting pagination while searching for %q", maxBranchPages, branchName)
}

func isNeonBranchReady(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "ready", "idle", "active":
		return true
	default:
		return false
	}
}

func (p *NeonDatabaseProvider) fetchConnectionURI(ctx context.Context, cfg *resolvedNeonConfig, branchID string, pooled bool) (string, error) {
	q := url.Values{}
	q.Set("branch_id", branchID)
	q.Set("database_name", cfg.databaseName)
	q.Set("role_name", cfg.roleName)
	if pooled {
		q.Set("pooled", "true")
	} else {
		q.Set("pooled", "false")
	}
	u := fmt.Sprintf("%s/projects/%s/connection_uri?%s", cfg.baseURL, url.PathEscape(cfg.projectID), q.Encode())
	var uriResp neonConnectionURIResponse
	status, raw, err := p.doJSON(ctx, cfg, http.MethodGet, u, nil, &uriResp)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("neon connection_uri lookup failed (%s)", extractNeonAPIError(status, raw))
	}
	uri := strings.TrimSpace(uriResp.URI)
	if uri == "" {
		return "", errors.New("neon connection_uri returned an empty URI")
	}
	return uri, nil
}

// Provision creates or reconciles an instant Copy-on-Write Neon Postgres branch for env.
func (p *NeonDatabaseProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*pkgdb.DatabaseResult, error) {
	if env == nil {
		return nil, errors.New("environment must not be nil")
	}
	cfg, err := p.resolveConfig(ctx, env)
	if err != nil {
		return nil, err
	}

	branchName := BranchName(env.Namespace, env.Name)
	branch, err := p.findBranchByName(ctx, cfg, branchName)
	if err != nil {
		return nil, err
	}

	if branch == nil {
		createURL := fmt.Sprintf("%s/projects/%s/branches", cfg.baseURL, url.PathEscape(cfg.projectID))
		createReq := neonCreateBranchRequest{
			Branch: neonBranchCreateSpec{
				Name:     branchName,
				ParentID: cfg.parentBranchID,
			},
			Endpoints: []neonEndpointTypeSpec{
				{Type: "read_write"},
			},
		}
		var createResp neonCreateBranchResponse
		status, raw, createErr := p.doJSON(ctx, cfg, http.MethodPost, createURL, createReq, &createResp)
		if createErr != nil {
			return nil, createErr
		}
		switch {
		case status >= 200 && status < 300:
			branch = &createResp.Branch
		case status == http.StatusLocked:
			// Neon returns 423 Locked when another branch/endpoint operation on the project is in flight.
			return &pkgdb.DatabaseResult{
				Ready:   false,
				Message: fmt.Sprintf("Neon project %s is busy with an active operation; queued branch %s", cfg.projectID, branchName),
			}, nil
		case status == http.StatusConflict || status == http.StatusUnprocessableEntity:
			// Concurrent reconcile may have just created the branch; re-list before failing.
			existing, findErr := p.findBranchByName(ctx, cfg, branchName)
			if findErr == nil && existing != nil {
				branch = existing
			} else {
				return nil, fmt.Errorf("neon create branch %q failed (%s)", branchName, extractNeonAPIError(status, raw))
			}
		default:
			return nil, fmt.Errorf("neon create branch %q failed (%s)", branchName, extractNeonAPIError(status, raw))
		}
	}

	if branch == nil || strings.TrimSpace(branch.ID) == "" {
		return nil, fmt.Errorf("neon branch %q returned empty branch ID", branchName)
	}
	if !safeNeonIDRegex.MatchString(strings.TrimSpace(branch.ID)) {
		return nil, fmt.Errorf("neon branch %q returned invalid branch ID %q", branchName, branch.ID)
	}

	if !isNeonBranchReady(branch.CurrentState) {
		state := strings.TrimSpace(branch.CurrentState)
		if state == "" {
			state = "provisioning"
		}
		return &pkgdb.DatabaseResult{
			Ready:   false,
			Message: fmt.Sprintf("Neon branch %s (%s) state is %s", branch.Name, branch.ID, state),
		}, nil
	}

	pooledURI, err := p.fetchConnectionURI(ctx, cfg, branch.ID, true)
	if err != nil {
		return nil, err
	}
	directURI, err := p.fetchConnectionURI(ctx, cfg, branch.ID, false)
	if err != nil {
		// Fall back to pooled URI if unpooled lookup is unsupported on the project tier.
		directURI = pooledURI
	}

	return &pkgdb.DatabaseResult{
		DSN:      pooledURI,
		AdminDSN: directURI,
		EnvVars: map[string]string{
			"DATABASE_URL":             pooledURI,
			"DATABASE_URL_DIRECT":      directURI,
			"DIVERGE_NEON_BRANCH_ID":   branch.ID,
			"DIVERGE_NEON_BRANCH_NAME": branch.Name,
			"DIVERGE_NEON_PROJECT_ID":  cfg.projectID,
		},
		Ready:   true,
		Message: fmt.Sprintf("Neon branch %s (%s) is ready", branch.Name, branch.ID),
	}, nil
}

// Status reports whether the preview environment's Neon Postgres branch exists and is ready.
func (p *NeonDatabaseProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*pkgdb.DatabaseStatus, error) {
	if env == nil {
		return nil, errors.New("environment must not be nil")
	}
	cfg, err := p.resolveConfig(ctx, env)
	if err != nil {
		return nil, err
	}

	branchName := BranchName(env.Namespace, env.Name)
	branch, err := p.findBranchByName(ctx, cfg, branchName)
	if err != nil {
		return nil, err
	}
	if branch == nil {
		return &pkgdb.DatabaseStatus{
			Provisioned: false,
			SchemaName:  branchName,
			Message:     fmt.Sprintf("Neon branch %s not found", branchName),
		}, nil
	}

	ready := isNeonBranchReady(branch.CurrentState)
	msg := fmt.Sprintf("Neon branch %s (%s) is ready", branch.Name, branch.ID)
	if !ready {
		msg = fmt.Sprintf("Neon branch %s (%s) state: %s", branch.Name, branch.ID, branch.CurrentState)
	}
	return &pkgdb.DatabaseStatus{
		Provisioned: ready,
		SchemaName:  branch.Name,
		Message:     msg,
	}, nil
}

// Teardown deletes the preview environment's Neon Postgres branch idempotently.
func (p *NeonDatabaseProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	if env == nil {
		return nil
	}
	cfg, err := p.resolveConfig(ctx, env)
	if err != nil {
		return err
	}

	branchName := BranchName(env.Namespace, env.Name)
	branch, err := p.findBranchByName(ctx, cfg, branchName)
	if err != nil {
		return err
	}
	if branch == nil || strings.TrimSpace(branch.ID) == "" {
		return nil
	}
	if !safeNeonIDRegex.MatchString(strings.TrimSpace(branch.ID)) {
		return fmt.Errorf("refusing to delete Neon branch with invalid ID %q", branch.ID)
	}

	delURL := fmt.Sprintf("%s/projects/%s/branches/%s", cfg.baseURL, url.PathEscape(cfg.projectID), url.PathEscape(branch.ID))
	status, raw, err := p.doJSON(ctx, cfg, http.MethodDelete, delURL, nil, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound || (status >= 200 && status < 300) {
		return nil
	}
	return fmt.Errorf("neon delete branch %q (%s) failed (%s)", branchName, branch.ID, extractNeonAPIError(status, raw))
}
