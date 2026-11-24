package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// ErrPolicyDenied marks authentication rejections enforced by policy (reserved
// principal prefixes, disallowed OIDC groups) where CompositeProvider must stop
// rather than falling through to subsequent providers.
var ErrPolicyDenied = errors.New("authentication denied by policy")

const (
	// DefaultOIDCPrefix prevents external OIDC/session claims from colliding with K8s/Diverge system principals.
	DefaultOIDCPrefix = "oidc:"
	// NoOIDCPrefix explicitly disables OIDC claim prefixing.
	NoOIDCPrefix = "-"
)

// OIDCProviderConfig configures the OIDC authentication provider.
type OIDCProviderConfig struct {
	// IssuerURL is the OIDC provider's issuer URL (used for discovery).
	IssuerURL string
	// ClientID is the OIDC client ID for token audience validation.
	ClientID string
	// UsernameClaim is the JWT claim used for the username. Defaults to "preferred_username".
	UsernameClaim string
	// GroupsClaim is the JWT claim used for group membership. Defaults to "groups".
	GroupsClaim string
	// AllowedGroups restricts access to users whose groups intersect this list.
	// Empty list means all authenticated users are allowed.
	AllowedGroups []string
	// UsernamePrefix is prepended to OIDC/session usernames (default "oidc:"; "-" disables).
	UsernamePrefix string
	// GroupsPrefix is prepended to OIDC/session groups (default "oidc:"; "-" disables).
	GroupsPrefix string
}

// OIDCProvider authenticates users by verifying OIDC ID tokens (JWTs).
// It supports both direct OIDC tokens and Diverge session JWTs.
type OIDCProvider struct {
	verifier       *oidc.IDTokenVerifier
	session        *SessionManager
	usernameClaim  string
	groupsClaim    string
	allowedGroups  map[string]bool
	usernamePrefix string
	groupsPrefix   string
	logger         *slog.Logger
}

func resolveOIDCPrefix(raw string) string {
	switch raw {
	case NoOIDCPrefix:
		return ""
	case "":
		return DefaultOIDCPrefix
	default:
		return raw
	}
}

// IsReservedOIDCPrincipal reports whether principal carries a reserved Kubernetes
// or Diverge prefix ("system:" or "divergedev:") that external OIDC/session tokens must not impersonate.
func IsReservedOIDCPrincipal(principal string) bool {
	return strings.HasPrefix(principal, "system:") || strings.HasPrefix(principal, "divergedev:")
}

// NewOIDCProvider creates a new OIDC authentication provider. It performs
// OpenID Connect discovery on the issuer URL to fetch the JWKS keys.
func NewOIDCProvider(ctx context.Context, cfg OIDCProviderConfig, session *SessionManager, logger *slog.Logger) (*OIDCProvider, error) {
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery failed for %q: %w", cfg.IssuerURL, err)
	}

	verifier := provider.Verifier(&oidc.Config{
		ClientID: cfg.ClientID,
	})

	usernameClaim := cfg.UsernameClaim
	if usernameClaim == "" {
		usernameClaim = "preferred_username"
	}
	groupsClaim := cfg.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}

	allowedGroups := make(map[string]bool, len(cfg.AllowedGroups))
	for _, g := range cfg.AllowedGroups {
		allowedGroups[g] = true
	}

	return &OIDCProvider{
		verifier:       verifier,
		session:        session,
		usernameClaim:  usernameClaim,
		groupsClaim:    groupsClaim,
		allowedGroups:  allowedGroups,
		usernamePrefix: resolveOIDCPrefix(cfg.UsernamePrefix),
		groupsPrefix:   resolveOIDCPrefix(cfg.GroupsPrefix),
		logger:         logger,
	}, nil
}

// Authenticate verifies the token as either a Diverge session JWT or an OIDC
// ID token. Returns UserInfo on success.
func (p *OIDCProvider) Authenticate(ctx context.Context, token string) (*UserInfo, error) {
	// Try session JWT first (fast, no network)
	if p.session != nil {
		if claims, err := p.session.Verify(token); err == nil {
			var expiresAt time.Time
			if claims.Expiry > 0 {
				expiresAt = time.Unix(claims.Expiry, 0)
			}
			return p.buildUserInfo(claims.Subject, "", claims.Email, claims.Groups, expiresAt, "session")
		}
	}

	if p.verifier == nil {
		return nil, fmt.Errorf("oidc verifier not configured")
	}

	// Try OIDC ID token verification (JWKS cached by go-oidc)
	idToken, err := p.verifier.Verify(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("oidc token verification failed: %w", err)
	}

	// Extract claims into a flexible map
	var rawClaims map[string]interface{}
	if err := idToken.Claims(&rawClaims); err != nil {
		return nil, fmt.Errorf("failed to parse oidc claims: %w", err)
	}

	username := ClaimString(rawClaims, p.usernameClaim)
	if username == "" {
		username = ClaimString(rawClaims, "email")
	}
	if username == "" {
		username = idToken.Subject
	}

	email := ClaimString(rawClaims, "email")
	groups := ClaimStringSlice(rawClaims, p.groupsClaim)

	return p.buildUserInfo(username, idToken.Subject, email, groups, idToken.Expiry, "oidc")
}

func (p *OIDCProvider) buildUserInfo(rawUsername, uid, email string, rawGroups []string, expiresAt time.Time, authType string) (*UserInfo, error) {
	if IsReservedOIDCPrincipal(rawUsername) {
		return nil, fmt.Errorf("%w: oidc username %q uses a reserved prefix", ErrPolicyDenied, rawUsername)
	}
	for _, g := range rawGroups {
		if IsReservedOIDCPrincipal(g) {
			return nil, fmt.Errorf("%w: oidc group %q uses a reserved prefix", ErrPolicyDenied, g)
		}
	}

	if err := p.checkAllowedGroups(rawGroups); err != nil {
		return nil, err
	}

	username := p.usernamePrefix + rawUsername
	if IsReservedOIDCPrincipal(username) {
		return nil, fmt.Errorf("%w: oidc username %q uses a reserved prefix", ErrPolicyDenied, username)
	}

	var groups []string
	if rawGroups != nil {
		groups = make([]string, len(rawGroups))
		for i, g := range rawGroups {
			prefixed := p.groupsPrefix + g
			if IsReservedOIDCPrincipal(prefixed) {
				return nil, fmt.Errorf("%w: oidc group %q uses a reserved prefix", ErrPolicyDenied, prefixed)
			}
			groups[i] = prefixed
		}
	}

	return &UserInfo{
		Username:  username,
		UID:       uid,
		Email:     email,
		Groups:    groups,
		ExpiresAt: expiresAt,
		AuthType:  authType,
	}, nil
}

func (p *OIDCProvider) checkAllowedGroups(groups []string) error {
	if len(p.allowedGroups) == 0 {
		return nil // No restriction
	}
	for _, g := range groups {
		if p.allowedGroups[g] {
			return nil
		}
	}
	return fmt.Errorf("%w: user not in any allowed group (allowed: %v)", ErrPolicyDenied, p.allowedGroups)
}

// ClaimString extracts a string claim from the raw claims map.
func ClaimString(claims map[string]interface{}, key string) string {
	if v, ok := claims[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ClaimStringSlice extracts a string slice claim from the raw claims map.
// Handles three formats:
//   - []string (standard OIDC)
//   - []interface{} (JSON-decoded array)
//   - map[string]interface{} (Zitadel project roles, where keys are role names)
func ClaimStringSlice(claims map[string]interface{}, key string) []string {
	v, ok := claims[key]
	if !ok {
		return nil
	}
	switch val := v.(type) {
	case []interface{}:
		result := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return val
	case map[string]interface{}:
		// Zitadel returns roles as: {"role_name": {"org_id": "..."}, ...}
		// Extract map keys as the role/group names.
		result := make([]string, 0, len(val))
		for k := range val {
			result = append(result, k)
		}
		slices.Sort(result)
		return result
	default:
		return nil
	}
}
