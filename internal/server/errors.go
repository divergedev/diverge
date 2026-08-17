package server

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// SanitizeK8sError maps Kubernetes API errors to safe Connect error codes.
// The raw K8s error is logged server-side but never returned to the client.
// Returns nil when err is nil.
func SanitizeK8sError(logger *slog.Logger, err error) error {
	if err == nil {
		return nil
	}

	switch {
	case apierrors.IsNotFound(err):
		logger.Debug("resource not found", "error", err)
		return connect.NewError(connect.CodeNotFound, errors.New("resource not found"))
	case apierrors.IsAlreadyExists(err):
		logger.Debug("resource already exists", "error", err)
		return connect.NewError(connect.CodeAlreadyExists, errors.New("resource already exists"))
	case apierrors.IsConflict(err):
		logger.Debug("resource conflict", "error", err)
		return connect.NewError(connect.CodeAborted, errors.New("resource version conflict, please retry with the latest version"))
	case apierrors.IsForbidden(err):
		logger.Warn("forbidden access", "error", err)
		return connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	case apierrors.IsUnauthorized(err):
		logger.Warn("unauthorized access", "error", err)
		return connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	case apierrors.IsInvalid(err):
		logger.Debug("invalid resource", "error", err)
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid resource specification"))
	case apierrors.IsBadRequest(err):
		logger.Debug("bad request", "error", err)
		return connect.NewError(connect.CodeInvalidArgument, errors.New("bad request"))
	case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err):
		logger.Error("request timeout", "error", err)
		return connect.NewError(connect.CodeDeadlineExceeded, errors.New("request timed out"))
	case apierrors.IsTooManyRequests(err):
		logger.Warn("rate limited", "error", err)
		return connect.NewError(connect.CodeResourceExhausted, errors.New("too many requests, please retry later"))
	case apierrors.IsServiceUnavailable(err):
		logger.Error("service unavailable", "error", err)
		return connect.NewError(connect.CodeUnavailable, errors.New("service temporarily unavailable"))
	default:
		if errors.Is(err, context.Canceled) {
			logger.Debug("request canceled", "error", err)
			return connect.NewError(connect.CodeCanceled, errors.New("request canceled"))
		}
		if errors.Is(err, context.DeadlineExceeded) {
			logger.Debug("deadline exceeded", "error", err)
			return connect.NewError(connect.CodeDeadlineExceeded, errors.New("request deadline exceeded"))
		}
		logger.Error("internal error", "error", err)
		return connect.NewError(connect.CodeInternal, errors.New("internal server error"))
	}
}
