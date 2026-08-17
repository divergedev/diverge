package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/divergedev/diverge/internal/server/auth"
)

// AuditLogger emits structured audit events for authentication, authorization,
// and resource mutations. All events are JSON-formatted via slog for
// compatibility with standard log aggregators.
type AuditLogger struct {
	logger *slog.Logger
}

// NewAuditLogger creates an audit logger wrapping the given structured logger.
func NewAuditLogger(logger *slog.Logger) *AuditLogger {
	return &AuditLogger{logger: logger.With("component", "audit")}
}

// LogAuth logs authentication events (success, failure, cache hit).
func (a *AuditLogger) LogAuth(ctx context.Context, event string, user *auth.UserInfo, r *http.Request, attrs ...slog.Attr) {
	allAttrs := []slog.Attr{
		slog.String("event", event),
		slog.String("source_ip", sourceIP(r)),
		slog.String("path", r.URL.Path),
	}
	if user != nil {
		allAttrs = append(allAttrs,
			slog.String("user", user.Username),
			slog.Any("groups", user.Groups),
		)
	}
	allAttrs = append(allAttrs, attrs...)

	level := slog.LevelInfo
	if event == "auth.failure" {
		level = slog.LevelWarn
	}
	a.logger.LogAttrs(ctx, level, "audit", allAttrs...)
}

// LogAuthz logs authorization events (denied, error).
func (a *AuditLogger) LogAuthz(ctx context.Context, event string, user *auth.UserInfo, verb, resource, namespace string) {
	allAttrs := []slog.Attr{
		slog.String("event", event),
		slog.String("verb", verb),
		slog.String("resource", resource),
		slog.String("namespace", namespace),
	}
	if user != nil {
		allAttrs = append(allAttrs,
			slog.String("user", user.Username),
			slog.Any("groups", user.Groups),
		)
	}

	level := slog.LevelInfo
	if event == "authz.denied" || event == "authz.error" {
		level = slog.LevelWarn
	}
	a.logger.LogAttrs(ctx, level, "audit", allAttrs...)
}

// LogMutation logs resource mutation events (create, update, delete).
func (a *AuditLogger) LogMutation(ctx context.Context, event string, resourceType, name, namespace string) {
	user, _ := auth.UserInfoFromContext(ctx)
	allAttrs := []slog.Attr{
		slog.String("event", event),
		slog.String("resource_type", resourceType),
		slog.String("name", name),
		slog.String("namespace", namespace),
	}
	if user != nil {
		allAttrs = append(allAttrs, slog.String("user", user.Username))
	}
	a.logger.LogAttrs(ctx, slog.LevelInfo, "audit", allAttrs...)
}

// sourceIP extracts the client IP from the request, preferring X-Forwarded-For.
func sourceIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}
