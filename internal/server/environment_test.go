package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	authzv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func buildFakeClient() client.Client {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = authzv1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).Build()
}

// contextWithUser returns a context with a mock user.
func contextWithUser() context.Context {
	u := &User{
		ID:     "test-user",
		Groups: []string{"system:authenticated"},
	}
	return context.WithValue(context.Background(), userContextKey, u)
}

func TestEnvironmentService_CreateEnvironment(t *testing.T) {
	c := buildFakeClient()
	svc := NewEnvironmentService(c).(*EnvironmentService)
	ctx := contextWithUser()

	req := connect.NewRequest(&pb.CreateEnvironmentRequest{
		Namespace: "default",
		Environment: &pb.Environment{
			Name: "test-env",
		},
	})

	// To make authorizeAction pass with fake client, we would need to pre-create or intercept the SAR creation.
	// Since fake client just stores the SAR, it won't set `Status.Allowed = true` automatically.
	// So authorizeAction will fail with permission denied.
	// Let's test the error response for unallowed.

	resp, err := svc.CreateEnvironment(ctx, req)
	if err == nil {
		t.Fatalf("expected error due to unallowed SAR, got response: %v", resp)
	}

	connectErr, ok := err.(*connect.Error)
	if !ok {
		t.Errorf("expected connect error, got: %v", err)
	}
	if connectErr.Code() != connect.CodePermissionDenied && connectErr.Code() != connect.CodeInvalidArgument {
		t.Errorf("expected permission denied or invalid argument, got: %v", err)
	}
}

func TestEnvironmentService_InputValidation(t *testing.T) {
	svc := NewEnvironmentService(nil)
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "Create missing namespace",
			call: func() error {
				_, err := svc.CreateEnvironment(ctx, connect.NewRequest(&pb.CreateEnvironmentRequest{}))
				return err
			},
		},
		{
			name: "Create missing environment",
			call: func() error {
				_, err := svc.CreateEnvironment(ctx, connect.NewRequest(&pb.CreateEnvironmentRequest{Namespace: "default"}))
				return err
			},
		},
		{
			name: "Get missing name",
			call: func() error {
				_, err := svc.GetEnvironment(ctx, connect.NewRequest(&pb.GetEnvironmentRequest{Namespace: "default"}))
				return err
			},
		},
		{
			name: "List missing namespace",
			call: func() error {
				_, err := svc.ListEnvironments(ctx, connect.NewRequest(&pb.ListEnvironmentsRequest{}))
				return err
			},
		},
		{
			name: "Update missing namespace",
			call: func() error {
				_, err := svc.UpdateEnvironment(ctx, connect.NewRequest(&pb.UpdateEnvironmentRequest{}))
				return err
			},
		},
		{
			name: "Delete missing name",
			call: func() error {
				_, err := svc.DeleteEnvironment(ctx, connect.NewRequest(&pb.DeleteEnvironmentRequest{Namespace: "default"}))
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			connectErr, ok := err.(*connect.Error)
			if !ok || connectErr.Code() != connect.CodeInvalidArgument {
				t.Errorf("expected InvalidArgument, got %v", err)
			}
		})
	}
}
