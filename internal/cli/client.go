package cli

import (
	"context"
	"io"

	divergev1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

// EnvironmentClient abstracts environment operations for CLI commands.
type EnvironmentClient interface {
	ListEnvironments(ctx context.Context, namespace string) ([]divergev1alpha1.Environment, error)
	GetEnvironment(ctx context.Context, namespace, name string) (*divergev1alpha1.Environment, error)
	DeleteEnvironment(ctx context.Context, namespace, name string) error
	StreamLogs(ctx context.Context, namespace, envName, service, container string, follow bool, tailLines int64, since string, timestamps bool, previous bool) (io.ReadCloser, error)
	ListPreviewGroups(ctx context.Context, namespace string) ([]divergev1alpha1.PreviewGroup, error)
}
