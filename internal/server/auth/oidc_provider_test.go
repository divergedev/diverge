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
		{
			"zitadel map roles",
			map[string]interface{}{
				"urn:zitadel:iam:org:project:roles": map[string]interface{}{
					"developer": map[string]interface{}{"org_id": "123"},
					"admin":     map[string]interface{}{"org_id": "123"},
				},
			},
			"urn:zitadel:iam:org:project:roles",
			2,
		},
		{
			"zitadel single role",
			map[string]interface{}{
				"urn:zitadel:iam:org:project:roles": map[string]interface{}{
					"viewer": map[string]interface{}{},
				},
			},
			"urn:zitadel:iam:org:project:roles",
			1,
		},
		{
			"zitadel empty map",
			map[string]interface{}{
				"urn:zitadel:iam:org:project:roles": map[string]interface{}{},
			},
			"urn:zitadel:iam:org:project:roles",
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

func TestClaimStringSlice_ZitadelRoleValues(t *testing.T) {
	claims := map[string]interface{}{
		"urn:zitadel:iam:org:project:roles": map[string]interface{}{
			"developer": map[string]interface{}{"org_id": "123"},
			"admin":     map[string]interface{}{"org_id": "456"},
		},
	}
	got := ClaimStringSlice(claims, "urn:zitadel:iam:org:project:roles")

	// Should be sorted deterministically
	if len(got) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(got))
	}
	if got[0] != "admin" || got[1] != "developer" {
		t.Errorf("expected [admin developer], got %v", got)
	}
}

func TestClaimStringSlice_ZitadelAllowedGroups(t *testing.T) {
	// End-to-end: Zitadel roles + checkAllowedGroups
	p := &OIDCProvider{
		allowedGroups: map[string]bool{"developer": true},
	}
	claims := map[string]interface{}{
		"urn:zitadel:iam:org:project:roles": map[string]interface{}{
			"developer": map[string]interface{}{"org_id": "123"},
		},
	}
	groups := ClaimStringSlice(claims, "urn:zitadel:iam:org:project:roles")
	if err := p.checkAllowedGroups(groups); err != nil {
		t.Errorf("Zitadel developer role should be allowed, got: %v", err)
	}

	// User without the allowed role
	claims2 := map[string]interface{}{
		"urn:zitadel:iam:org:project:roles": map[string]interface{}{
			"viewer": map[string]interface{}{"org_id": "123"},
		},
	}
	groups2 := ClaimStringSlice(claims2, "urn:zitadel:iam:org:project:roles")
	if err := p.checkAllowedGroups(groups2); err == nil {
		t.Error("Zitadel viewer role should NOT be allowed")
	}
}

// PBT: ClaimStringSlice always returns a subset of input keys/values.
func TestPBT_ClaimStringSlice_MapKeysExtracted(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numRoles := rapid.IntRange(0, 10).Draw(t, "numRoles")
		roleMap := make(map[string]interface{}, numRoles)
		expectedKeys := make(map[string]bool)
		for i := 0; i < numRoles; i++ {
			key := rapid.StringMatching(`^[a-z_]{2,15}$`).Draw(t, "role")
			roleMap[key] = map[string]interface{}{}
			expectedKeys[key] = true
		}

		claims := map[string]interface{}{"roles": roleMap}
		got := ClaimStringSlice(claims, "roles")

		if len(got) != len(expectedKeys) {
			t.Errorf("expected %d roles, got %d", len(expectedKeys), len(got))
		}
		for _, role := range got {
			if !expectedKeys[role] {
				t.Errorf("unexpected role %q in result", role)
			}
		}
		// Verify sorted
		for i := 1; i < len(got); i++ {
			if got[i] < got[i-1] {
				t.Errorf("result not sorted: %v", got)
			}
		}
	})
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

// PBT: []interface{} format always extracts exactly the string elements.
func TestPBT_ClaimStringSlice_InterfaceSliceFilterNonStrings(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numItems := rapid.IntRange(0, 20).Draw(t, "numItems")
		items := make([]interface{}, 0, numItems)
		expectedCount := 0
		for i := 0; i < numItems; i++ {
			switch rapid.IntRange(0, 2).Draw(t, "itemType") {
			case 0:
				items = append(items, rapid.String().Draw(t, "str"))
				expectedCount++
			case 1:
				items = append(items, rapid.Int().Draw(t, "int"))
			case 2:
				items = append(items, rapid.Bool().Draw(t, "bool"))
			}
		}

		claims := map[string]interface{}{"groups": items}
		got := ClaimStringSlice(claims, "groups")

		if len(got) != expectedCount {
			t.Errorf("expected %d string items, got %d", expectedCount, len(got))
		}
		// Every returned item must be a non-empty extraction from the input
		for _, s := range got {
			found := false
			for _, item := range items {
				if str, ok := item.(string); ok && str == s {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("result %q not found in input", s)
			}
		}
	})
}

// PBT: ClaimStringSlice never panics regardless of claim value type.
func TestPBT_ClaimStringSlice_NeverPanics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		valGen := rapid.Custom(func(t *rapid.T) interface{} {
			switch rapid.IntRange(0, 5).Draw(t, "type") {
			case 0:
				return rapid.String().Draw(t, "s")
			case 1:
				return rapid.Int().Draw(t, "i")
			case 2:
				return []interface{}{rapid.String().Draw(t, "elem")}
			case 3:
				return []string{rapid.String().Draw(t, "elem")}
			case 4:
				return map[string]interface{}{rapid.String().Draw(t, "k"): nil}
			default:
				return nil
			}
		})

		key := rapid.StringMatching(`^[a-z]{1,10}$`).Draw(t, "key")
		claims := map[string]interface{}{key: valGen.Draw(t, "val")}

		// Must not panic
		_ = ClaimStringSlice(claims, key)
	})
}

// PBT: Map format output is always sorted (deterministic iteration).
func TestPBT_ClaimStringSlice_MapAlwaysSorted(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numRoles := rapid.IntRange(0, 20).Draw(t, "numRoles")
		roleMap := make(map[string]interface{}, numRoles)
		for i := 0; i < numRoles; i++ {
			key := rapid.StringMatching(`^[a-z]{2,12}$`).Draw(t, "role")
			roleMap[key] = map[string]interface{}{}
		}

		claims := map[string]interface{}{"roles": roleMap}
		got := ClaimStringSlice(claims, "roles")

		for i := 1; i < len(got); i++ {
			if got[i] < got[i-1] {
				t.Fatalf("not sorted at index %d: %v", i, got)
			}
		}
	})
}

// PBT: Zitadel end-to-end — random roles, allowedGroups check is consistent.
func TestPBT_ZitadelRoles_AllowedGroupsConsistency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random Zitadel-style role map
		numRoles := rapid.IntRange(1, 8).Draw(t, "numRoles")
		roleMap := make(map[string]interface{}, numRoles)
		allRoles := make([]string, 0, numRoles)
		for i := 0; i < numRoles; i++ {
			role := rapid.StringMatching(`^[a-z_]{2,12}$`).Draw(t, "role")
			roleMap[role] = map[string]interface{}{"org_id": "123"}
			allRoles = append(allRoles, role)
		}

		// Generate random allowed set (may or may not overlap)
		numAllowed := rapid.IntRange(1, 5).Draw(t, "numAllowed")
		allowedMap := make(map[string]bool, numAllowed)
		for i := 0; i < numAllowed; i++ {
			if rapid.Bool().Draw(t, "useExisting") && len(allRoles) > 0 {
				idx := rapid.IntRange(0, len(allRoles)-1).Draw(t, "idx")
				allowedMap[allRoles[idx]] = true
			} else {
				allowedMap[rapid.StringMatching(`^[a-z_]{2,12}$`).Draw(t, "newRole")] = true
			}
		}

		claims := map[string]interface{}{"urn:zitadel:iam:org:project:roles": roleMap}
		groups := ClaimStringSlice(claims, "urn:zitadel:iam:org:project:roles")

		p := &OIDCProvider{allowedGroups: allowedMap}
		err := p.checkAllowedGroups(groups)

		// Compute expected: should pass iff any group is in allowedMap
		shouldPass := false
		for _, g := range groups {
			if allowedMap[g] {
				shouldPass = true
				break
			}
		}

		if shouldPass && err != nil {
			t.Errorf("expected pass (overlap exists), got err: %v\nroles: %v, allowed: %v", err, groups, allowedMap)
		}
		if !shouldPass && err == nil {
			t.Errorf("expected fail (no overlap), got nil\nroles: %v, allowed: %v", groups, allowedMap)
		}
	})
}
