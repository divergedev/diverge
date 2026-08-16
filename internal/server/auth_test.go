package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
)

func TestNewAuthInterceptor(t *testing.T) {
	interceptor := NewAuthInterceptor()

	// Create a dummy next function
	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		u, ok := UserFromContext(ctx)
		if !ok {
			t.Fatal("user not found in context")
		}
		if u.ID != "admin" {
			t.Errorf("expected user ID 'admin', got '%s'", u.ID)
		}
		if len(u.Groups) != 1 || u.Groups[0] != "system:masters" {
			t.Errorf("expected user groups ['system:masters'], got '%v'", u.Groups)
		}
		return nil, nil
	})

	wrapped := interceptor(next)

	tests := []struct {
		name        string
		authHeader  string
		expectError bool
		errCode     connect.Code
	}{
		{
			name:        "missing header",
			authHeader:  "",
			expectError: true,
			errCode:     connect.CodeUnauthenticated,
		},
		{
			name:        "invalid format",
			authHeader:  "Basic something",
			expectError: true,
			errCode:     connect.CodeUnauthenticated,
		},
		{
			name:        "invalid token",
			authHeader:  "Bearer bad-token",
			expectError: true,
			errCode:     connect.CodeUnauthenticated,
		},
		{
			name:        "valid token",
			authHeader:  "Bearer valid-token",
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := connect.NewRequest(&struct{}{})
			if tc.authHeader != "" {
				req.Header().Set("Authorization", tc.authHeader)
			}

			_, err := wrapped(context.Background(), req)

			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				connectErr, ok := err.(*connect.Error)
				if !ok {
					t.Fatalf("expected connect.Error, got %T", err)
				}
				if connectErr.Code() != tc.errCode {
					t.Errorf("expected error code %v, got %v", tc.errCode, connectErr.Code())
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}
		})
	}
}
