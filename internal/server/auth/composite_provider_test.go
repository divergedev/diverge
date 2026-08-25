package auth

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

// compositeTestProvider is a test double for AuthProvider.
type compositeTestProvider struct {
	user *UserInfo
	err  error
}

func (m *compositeTestProvider) Authenticate(_ context.Context, _ string) (*UserInfo, error) {
	return m.user, m.err
}

func TestCompositeProvider_FirstProviderSucceeds(t *testing.T) {
	cp := NewCompositeProvider(slog.Default())
	cp.Add("oidc", &compositeTestProvider{user: &UserInfo{Username: "alice"}, err: nil})
	cp.Add("tokenreview", &compositeTestProvider{user: &UserInfo{Username: "bob"}, err: nil})

	user, err := cp.Authenticate(context.Background(), "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.Username != "alice" {
		t.Errorf("Username = %q, want %q", user.Username, "alice")
	}
}

func TestCompositeProvider_FallbackOnFailure(t *testing.T) {
	cp := NewCompositeProvider(slog.Default())
	cp.Add("oidc", &compositeTestProvider{err: errors.New("oidc: invalid token")})
	cp.Add("tokenreview", &compositeTestProvider{user: &UserInfo{Username: "bob"}, err: nil})

	user, err := cp.Authenticate(context.Background(), "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.Username != "bob" {
		t.Errorf("Username = %q, want %q", user.Username, "bob")
	}
}

func TestCompositeProvider_AllFail(t *testing.T) {
	cp := NewCompositeProvider(slog.Default())
	cp.Add("oidc", &compositeTestProvider{err: errors.New("oidc: invalid token")})
	cp.Add("tokenreview", &compositeTestProvider{err: errors.New("tokenreview: rejected")})

	_, err := cp.Authenticate(context.Background(), "token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCompositeProvider_NoProviders(t *testing.T) {
	cp := NewCompositeProvider(slog.Default())

	_, err := cp.Authenticate(context.Background(), "token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
