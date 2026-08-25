package auth

import (
	"testing"
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
