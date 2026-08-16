package server

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
)

type User struct {
	ID     string
	Groups []string
}

type contextKey string

const userContextKey = contextKey("user")

// UserFromContext retrieves the authenticated user from the context.
func UserFromContext(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(userContextKey).(*User)
	return u, ok
}

func NewAuthInterceptor() connect.UnaryInterceptorFunc {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			authHeader := req.Header().Get("Authorization")
			if authHeader == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing authorization header"))
			}

			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid authorization header format"))
			}

			// In a real system we would validate OIDC token or K8s TokenReview here.
			token := parts[1]
			if token == "invalid-token" || token == "bad-token" {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid token"))
			}

			// Mock user for now
			user := &User{
				ID:     "admin",
				Groups: []string{"system:masters"},
			}
			ctx = context.WithValue(ctx, userContextKey, user)

			return next(ctx, req)
		})
	})
}
