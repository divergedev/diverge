package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"pgregory.net/rapid"
)

func TestSanitizeK8sError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	gr := schema.GroupResource{Group: "apps", Resource: "deployments"}

	tests := []struct {
		name         string
		err          error
		expectedCode connect.Code
		expectedMsg  string
	}{
		{
			name:         "not found",
			err:          apierrors.NewNotFound(gr, "my-deploy"),
			expectedCode: connect.CodeNotFound,
			expectedMsg:  "resource not found",
		},
		{
			name:         "already exists",
			err:          apierrors.NewAlreadyExists(gr, "my-deploy"),
			expectedCode: connect.CodeAlreadyExists,
			expectedMsg:  "resource already exists",
		},
		{
			name:         "conflict",
			err:          apierrors.NewConflict(gr, "my-deploy", errors.New("conflict")),
			expectedCode: connect.CodeAborted,
			expectedMsg:  "resource version conflict, please retry with the latest version",
		},
		{
			name:         "forbidden",
			err:          apierrors.NewForbidden(gr, "my-deploy", errors.New("forbidden")),
			expectedCode: connect.CodePermissionDenied,
			expectedMsg:  "permission denied",
		},
		{
			name:         "unauthorized",
			err:          apierrors.NewUnauthorized("unauthorized"),
			expectedCode: connect.CodeUnauthenticated,
			expectedMsg:  "not authenticated",
		},
		{
			name:         "invalid",
			err:          apierrors.NewInvalid(schema.GroupKind{Group: "apps", Kind: "Deployment"}, "my-deploy", nil),
			expectedCode: connect.CodeInvalidArgument,
			expectedMsg:  "invalid resource specification",
		},
		{
			name:         "bad request",
			err:          apierrors.NewBadRequest("bad request"),
			expectedCode: connect.CodeInvalidArgument,
			expectedMsg:  "bad request",
		},
		{
			name:         "timeout",
			err:          apierrors.NewTimeoutError("timeout", 10),
			expectedCode: connect.CodeDeadlineExceeded,
			expectedMsg:  "request timed out",
		},
		{
			name:         "server timeout",
			err:          apierrors.NewServerTimeout(gr, "get", 2),
			expectedCode: connect.CodeDeadlineExceeded,
			expectedMsg:  "request timed out",
		},
		{
			name:         "too many requests",
			err:          apierrors.NewTooManyRequestsError("rate limited"),
			expectedCode: connect.CodeResourceExhausted,
			expectedMsg:  "too many requests, please retry later",
		},
		{
			name:         "service unavailable",
			err:          apierrors.NewServiceUnavailable("unavailable"),
			expectedCode: connect.CodeUnavailable,
			expectedMsg:  "service temporarily unavailable",
		},
		{
			name:         "context canceled",
			err:          context.Canceled,
			expectedCode: connect.CodeCanceled,
			expectedMsg:  "request canceled",
		},
		{
			name:         "context deadline exceeded",
			err:          context.DeadlineExceeded,
			expectedCode: connect.CodeDeadlineExceeded,
			expectedMsg:  "request deadline exceeded",
		},
		{
			name:         "unknown error",
			err:          errors.New("some random error"),
			expectedCode: connect.CodeInternal,
			expectedMsg:  "internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := SanitizeK8sError(logger, tt.err)
			require.NotNil(t, res)
			var connectErr *connect.Error
			require.True(t, errors.As(res, &connectErr))
			assert.Equal(t, tt.expectedCode, connectErr.Code())
			assert.Equal(t, tt.expectedMsg, connectErr.Message())
		})
	}
}

func TestSanitizeK8sError_NilReturnsNil(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	res := SanitizeK8sError(logger, nil)
	assert.Nil(t, res)
}

func TestSanitizeK8sError_PBT(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	rapid.Check(t, func(t *rapid.T) {
		errMsg := rapid.StringMatching(`^[a-zA-Z0-9 _-]{10,100}$`).Draw(t, "errMsg")

		err := errors.New(errMsg)

		choice := rapid.IntRange(0, 5).Draw(t, "choice")
		var k8sErr error
		gr := schema.GroupResource{Group: "apps", Resource: "deployments"}
		switch choice {
		case 0:
			k8sErr = apierrors.NewNotFound(gr, errMsg)
		case 1:
			k8sErr = apierrors.NewConflict(gr, "my-deploy", err)
		case 2:
			k8sErr = apierrors.NewForbidden(gr, "my-deploy", err)
		case 3:
			k8sErr = apierrors.NewUnauthorized(errMsg)
		case 4:
			k8sErr = apierrors.NewBadRequest(errMsg)
		case 5:
			k8sErr = err
		}

		res := SanitizeK8sError(logger, k8sErr)
		require.NotNil(t, res)

		assert.False(t, strings.Contains(res.Error(), errMsg), "Output should not leak input error message")
	})
}
