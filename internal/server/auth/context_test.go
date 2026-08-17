package auth

import (
	"context"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	"pgregory.net/rapid"
)

func TestContextWithUserInfo(t *testing.T) {
	ctx := context.Background()

	// Initial context should have no user info
	_, ok := UserInfoFromContext(ctx)
	if ok {
		t.Fatal("expected no UserInfo in empty context")
	}

	info := &UserInfo{
		Username: "alice",
		UID:      "123",
		Groups:   []string{"admin", "dev"},
		Extra: map[string]authorizationv1.ExtraValue{
			"scopes": {"read", "write"},
		},
	}

	ctxWithInfo := ContextWithUserInfo(ctx, info)

	// Context with info should return it
	retrieved, ok := UserInfoFromContext(ctxWithInfo)
	if !ok {
		t.Fatal("expected UserInfo in context")
	}

	if retrieved.Username != "alice" {
		t.Errorf("expected Username 'alice', got %q", retrieved.Username)
	}
	if retrieved.UID != "123" {
		t.Errorf("expected UID '123', got %q", retrieved.UID)
	}
	if len(retrieved.Groups) != 2 || retrieved.Groups[0] != "admin" || retrieved.Groups[1] != "dev" {
		t.Errorf("expected Groups ['admin', 'dev'], got %v", retrieved.Groups)
	}
	if len(retrieved.Extra["scopes"]) != 2 || retrieved.Extra["scopes"][0] != "read" || retrieved.Extra["scopes"][1] != "write" {
		t.Errorf("expected Extra scopes ['read', 'write'], got %v", retrieved.Extra["scopes"])
	}
}

func TestUserInfo_DeepCopy_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		uid := rapid.String().Draw(t, "uid")
		groups := rapid.SliceOf(rapid.String()).Draw(t, "groups")

		keys := rapid.SliceOf(rapid.String()).Draw(t, "keys")
		extra := make(map[string]authorizationv1.ExtraValue)
		for _, k := range keys {
			vals := rapid.SliceOf(rapid.String()).Draw(t, "vals_"+k)
			extra[k] = authorizationv1.ExtraValue(vals)
		}

		orig := &UserInfo{
			Username: "test-user",
			UID:      uid,
			Groups:   groups,
			Extra:    extra,
		}

		copied := orig.DeepCopy()

		if copied.UID != orig.UID {
			t.Fatalf("UID mismatch")
		}
		if len(copied.Groups) != len(orig.Groups) {
			t.Fatalf("Groups length mismatch")
		}

		if orig.Extra == nil {
			orig.Extra = make(map[string]authorizationv1.ExtraValue)
		}
		orig.Extra["mutated"] = authorizationv1.ExtraValue{"mutated-val"}
		if len(keys) > 0 {
			orig.Extra[keys[0]] = append(orig.Extra[keys[0]], "extra-mutation")
		}

		if _, ok := copied.Extra["mutated"]; ok {
			t.Fatalf("Copy was affected by mutation")
		}
	})
}
