package server

import (
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// toConnectError converts K8s API errors to safe ConnectRPC errors.
func toConnectError(err error) error {
	if apierrors.IsNotFound(err) {
		return connect.NewError(connect.CodeNotFound, errors.New("resource not found"))
	}
	if apierrors.IsAlreadyExists(err) {
		return connect.NewError(connect.CodeAlreadyExists, errors.New("resource already exists"))
	}
	if apierrors.IsForbidden(err) {
		return connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}
	if apierrors.IsConflict(err) {
		return connect.NewError(connect.CodeAborted, errors.New("resource conflict, please retry"))
	}
	if apierrors.IsInvalid(err) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid resource specification"))
	}
	// Log the real error internally, return generic message to client
	slog.Error("internal error", "error", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal server error"))
}
