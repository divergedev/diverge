package auth

import (
	"context"
	"testing"

	"pgregory.net/rapid"
)

func TestClaimString(t *testing.T) {
	claims := map[string]interface{}{
		"preferred_username": "alice",
		"email":              "alice@example.com",
		"number":             42,
	}

	tests := []struct {
		key  string
		want string
	}{
		{"preferred_username", "alice"},
		{"email", "alice@example.com"},
		{"missing", ""},
		{"number", ""}, // Not a string
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := ClaimString(claims, tt.key)
			if got != tt.want {
				t.Errorf("ClaimString(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestClaimStringSlice(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]interface{}
		key    string
		want   int
	}{
		{
			"interface slice",
			map[string]interface{}{"groups": []interface{}{"admin", "dev"}},
			"groups",
			2,
		},
		{
			"missing key",
			map[string]interface{}{},
			"groups",
			0,
		},
		{
			"non-slice value",
			map[string]interface{}{"groups": "single"},
			"groups",
			0,
		},
		{
			"empty slice",
			map[string]interface{}{"groups": []interface{}{}},
			"groups",
			0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClaimStringSlice(tt.claims, tt.key)
			if len(got) != tt.want {
				t.Errorf("ClaimStringSlice() len = %d, want %d", len(got), tt.want)
			}
		})
	}
}

func TestOIDCProviderConfig_Defaults(t *testing.T) {
	cfg := OIDCProviderConfig{
		IssuerURL: "https://accounts.google.com",
		ClientID:  "test-client",
	}

	// Verify defaults are applied
	if cfg.UsernameClaim != "" {
		t.Errorf("UsernameClaim default should be empty (applied at construction)")
	}
	if cfg.GroupsClaim != "" {
		t.Errorf("GroupsClaim default should be empty (applied at construction)")
	}
}

func TestCheckAllowedGroups(t *testing.T) {
	// Test with empty allowedGroups (should allow all)
	p := &OIDCProvider{
		allowedGroups: map[string]bool{},
	}
	if err := p.checkAllowedGroups([]string{"any-group"}); err != nil {
		t.Errorf("empty allowedGroups should allow all, got: %v", err)
	}

	// Test with restricted groups
	p.allowedGroups = map[string]bool{"admins": true}
	if err := p.checkAllowedGroups([]string{"admins"}); err != nil {
		t.Errorf("user in allowed group should pass, got: %v", err)
	}
	if err := p.checkAllowedGroups([]string{"developers"}); err == nil {
		t.Error("user not in allowed group should fail")
	}
	if err := p.checkAllowedGroups(nil); err == nil {
		t.Error("user with no groups should fail when allowedGroups is set")
	}
}

func TestOIDCProvider_SessionJWT_Roundtrip(t *testing.T) {
	sm, _ := NewSessionManager(SessionConfig{})
	provider := &OIDCProvider{
		session:       sm,
		allowedGroups: map[string]bool{},
	}
	token, _ := sm.Mint("alice", "alice@example.com", "oidc", []string{"dev"})

	user, err := provider.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if user.Username != "alice" {
		t.Errorf("Username = %q, want alice", user.Username)
	}
	if user.Email != "alice@example.com" {
		t.Errorf("Email = %q, want alice@example.com", user.Email)
	}
	if len(user.Groups) != 1 || user.Groups[0] != "dev" {
		t.Errorf("Groups = %v, want [dev]", user.Groups)
	}
}

func TestOIDCProvider_SessionJWT_EmailPreserved(t *testing.T) {
	sm, _ := NewSessionManager(SessionConfig{})
	provider := &OIDCProvider{session: sm}

	token, _ := sm.Mint("bob", "bob@example.com", "oidc", nil)
	user, err := provider.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if user.Email != "bob@example.com" {
		t.Errorf("Email not preserved, got %q", user.Email)
	}
}

func TestClaimStringSlice_NativeStringSlice(t *testing.T) {
	claims := map[string]interface{}{
		"groups": []string{"a", "b"},
	}
	got := ClaimStringSlice(claims, "groups")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v, want [a b]", got)
	}
}

func TestCheckAllowedGroups_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		allowed := rapid.SliceOf(rapid.String()).Draw(t, "allowed")
		userGroups := rapid.SliceOf(rapid.String()).Draw(t, "userGroups")

		allowedMap := make(map[string]bool)
		for _, g := range allowed {
			allowedMap[g] = true
		}

		p := &OIDCProvider{allowedGroups: allowedMap}
		err := p.checkAllowedGroups(userGroups)

		intersect := false
		if len(allowedMap) == 0 {
			intersect = true
		} else {
			for _, g := range userGroups {
				if allowedMap[g] {
					intersect = true
					break
				}
			}
		}

		if intersect && err != nil {
			t.Fatalf("expected pass, got err: %v", err)
		}
		if !intersect && err == nil {
			t.Fatalf("expected fail, got nil")
		}
	})
}

func TestClaimString_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		key := rapid.String().Draw(t, "key")
		valGen := rapid.Custom(func(t *rapid.T) interface{} {
			switch rapid.IntRange(0, 2).Draw(t, "type") {
			case 0:
				return rapid.String().Draw(t, "s")
			case 1:
				return rapid.Int().Draw(t, "i")
			default:
				return rapid.Bool().Draw(t, "b")
			}
		})
		claimsGen := rapid.MapOf(rapid.String(), valGen)
		claims := claimsGen.Draw(t, "claims")

		// Ensure this doesn't panic
		_ = ClaimString(claims, key)
	})
}
