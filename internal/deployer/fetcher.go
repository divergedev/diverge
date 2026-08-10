package deployer

import (
	"context"

	"github.com/divergedev/diverge/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ManifestFetcher retrieves pre-rendered Kubernetes manifests for deployment.
type ManifestFetcher interface {
	Fetch(ctx context.Context, env *v1alpha1.Environment) ([]unstructured.Unstructured, error)
}
