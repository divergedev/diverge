package server

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestToConnectError(t *testing.T) {
	gr := schema.GroupResource{Group: "diverge.dev", Resource: "environments"}

	tests := []struct {
		name     string
		input    error
		expected connect.Code
	}{
		{
			name:     "NotFound",
			input:    apierrors.NewNotFound(gr, "env1"),
			expected: connect.CodeNotFound,
		},
		{
			name:     "AlreadyExists",
			input:    apierrors.NewAlreadyExists(gr, "env1"),
			expected: connect.CodeAlreadyExists,
		},
		{
			name:     "Forbidden",
			input:    apierrors.NewForbidden(gr, "env1", errors.New("forbidden")),
			expected: connect.CodePermissionDenied,
		},
		{
			name:     "Conflict",
			input:    apierrors.NewConflict(gr, "env1", errors.New("conflict")),
			expected: connect.CodeAborted,
		},
		{
			name:     "Invalid",
			input:    apierrors.NewInvalid(schema.GroupKind{Group: "diverge.dev", Kind: "Environment"}, "env1", nil),
			expected: connect.CodeInvalidArgument,
		},
		{
			name:     "Generic error",
			input:    errors.New("some other error"),
			expected: connect.CodeInternal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := toConnectError(tc.input)
			connectErr, ok := err.(*connect.Error)
			if !ok {
				t.Fatalf("expected connect.Error, got %T", err)
			}
			if connectErr.Code() != tc.expected {
				t.Errorf("expected code %v, got %v", tc.expected, connectErr.Code())
			}
		})
	}
}
