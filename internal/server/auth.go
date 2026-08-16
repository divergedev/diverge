package server

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
)

func extractBearerToken(authHeader string) string {
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return ""
	}
	return parts[1]
}

type authInterceptor struct {
	authAttempts *prometheus.CounterVec
}

func (a *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		token := extractBearerToken(req.Header().Get("Authorization"))
		provider := "bearer"
		if token == "" {
			a.authAttempts.WithLabelValues(provider, "failure").Inc()
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing authorization header or invalid format"))
		}
		if token == "invalid-token" {
			a.authAttempts.WithLabelValues(provider, "failure").Inc()
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid token"))
		}
		a.authAttempts.WithLabelValues(provider, "success").Inc()
		return next(ctx, req)
	}
}

func (a *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (a *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		token := extractBearerToken(conn.RequestHeader().Get("Authorization"))
		provider := "bearer"
		if token == "" {
			a.authAttempts.WithLabelValues(provider, "failure").Inc()
			return connect.NewError(connect.CodeUnauthenticated, errors.New("missing authorization header or invalid format"))
		}
		if token == "invalid-token" {
			a.authAttempts.WithLabelValues(provider, "failure").Inc()
			return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid token"))
		}
		a.authAttempts.WithLabelValues(provider, "success").Inc()
		return next(ctx, conn)
	}
}

func NewAuthInterceptor(authAttempts *prometheus.CounterVec) connect.Interceptor {
	return &authInterceptor{
		authAttempts: authAttempts,
	}
}
