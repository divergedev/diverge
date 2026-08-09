package routing

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type Router interface {
	Reconcile(ctx context.Context, env *v1alpha1.Environment) error
	Teardown(ctx context.Context, env *v1alpha1.Environment) error
	GetExternalURL(env *v1alpha1.Environment) string
}
