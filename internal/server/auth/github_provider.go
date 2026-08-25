package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// GitHubProviderConfig configures the GitHub OAuth authentication provider.
type GitHubProviderConfig struct {
	// AllowedOrgs restricts access to members of these GitHub organizations.
	// Empty list means all authenticated GitHub users are allowed.
	AllowedOrgs []string
	// AllowedGroups restricts access to users in these groups (org:team format).
	// Applied after session JWT verification.
	AllowedGroups []string
}

// GitHubProvider authenticates users via GitHub OAuth2 access tokens.
// It fetches the user's profile and team memberships to build UserInfo.
type GitHubProvider struct {
	httpClient    *http.Client
	session       *SessionManager
	allowedOrgs   map[string]bool
	allowedGroups map[string]bool
	logger        *slog.Logger
}

// NewGitHubProvider creates a new GitHub authentication provider.
func NewGitHubProvider(cfg GitHubProviderConfig, session *SessionManager, logger *slog.Logger) *GitHubProvider {
	allowedOrgs := make(map[string]bool, len(cfg.AllowedOrgs))
	for _, org := range cfg.AllowedOrgs {
		allowedOrgs[org] = true
	}
	allowedGroups := make(map[string]bool, len(cfg.AllowedGroups))
	for _, g := range cfg.AllowedGroups {
		allowedGroups[g] = true
	}
	return &GitHubProvider{
		httpClient:    http.DefaultClient,
		session:       session,
		allowedOrgs:   allowedOrgs,
		allowedGroups: allowedGroups,
		logger:        logger,
	}
}

// Authenticate verifies the token as either a Diverge session JWT or a
// GitHub personal access token. Returns UserInfo on success.
func (p *GitHubProvider) Authenticate(ctx context.Context, token string) (*UserInfo, error) {
	// Try session JWT first
	if claims, err := p.session.Verify(token); err == nil {
		user := &UserInfo{
			Username: claims.Subject,
			Groups:   claims.Groups,
		}
		if err := p.checkAllowedGroups(user.Groups); err != nil {
			return nil, err
		}
		return user, nil
	}

	// Fetch GitHub user profile
	ghUser, err := p.fetchUser(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("github user fetch failed: %w", err)
	}

	// Fetch team memberships for group claims
	teams, err := p.fetchTeams(ctx, token)
	if err != nil {
		p.logger.Warn("failed to fetch GitHub teams, continuing without groups", "error", err)
	}

	groups := make([]string, 0, len(teams))
	for _, team := range teams {
		groups = append(groups, team.Org.Login+":"+team.Slug)
	}

	// Check org membership if restricted
	if len(p.allowedOrgs) > 0 {
		orgs := make(map[string]bool)
		for _, team := range teams {
			orgs[team.Org.Login] = true
		}
		allowed := false
		for org := range p.allowedOrgs {
			if orgs[org] {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("github user %q is not a member of any allowed organization", ghUser.Login)
		}
	}

	return &UserInfo{
		Username: ghUser.Login,
		UID:      fmt.Sprintf("%d", ghUser.ID),
		Groups:   groups,
	}, nil
}

func (p *GitHubProvider) checkAllowedGroups(groups []string) error {
	if len(p.allowedGroups) == 0 {
		return nil
	}
	for _, g := range groups {
		if p.allowedGroups[g] {
			return nil
		}
	}
	return fmt.Errorf("user not in any allowed group")
}

type githubUser struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type githubTeam struct {
	Slug string `json:"slug"`
	Org  struct {
		Login string `json:"login"`
	} `json:"organization"`
}

func (p *GitHubProvider) fetchUser(ctx context.Context, token string) (*githubUser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github api returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var user githubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("failed to decode github user: %w", err)
	}
	return &user, nil
}

func (p *GitHubProvider) fetchTeams(ctx context.Context, token string) ([]githubTeam, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user/teams?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github teams api request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github teams api returned %d", resp.StatusCode)
	}

	var teams []githubTeam
	if err := json.NewDecoder(resp.Body).Decode(&teams); err != nil {
		return nil, fmt.Errorf("failed to decode github teams: %w", err)
	}
	return teams, nil
}
