package auth

import (
	"context"
	"testing"
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
}
