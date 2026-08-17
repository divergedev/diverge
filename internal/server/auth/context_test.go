package auth

import (
	"context"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
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
