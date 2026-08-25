package otel

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/discovery"
)

// IsOperatorInstalled checks whether the OpenTelemetry Operator is present
// by looking for the Instrumentation CRD via the discovery API.
func IsOperatorInstalled(ctx context.Context, disc discovery.DiscoveryInterface) (bool, string, error) {
	// Check for v1alpha2 first
	found, err := checkGroupVersion(disc, "opentelemetry.io/v1alpha2")
	if err != nil {
		return false, "", err
	}
	if found {
		return true, "opentelemetry.io/v1alpha2", nil
	}

	// Fallback to v1alpha1
	found, err = checkGroupVersion(disc, "opentelemetry.io/v1alpha1")
	if err != nil {
		return false, "", err
	}
	if found {
		return true, "opentelemetry.io/v1alpha1", nil
	}

	return false, "", nil
}

func checkGroupVersion(disc discovery.DiscoveryInterface, groupVersion string) (bool, error) {
	resources, err := disc.ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		// Ignore NotFound error
		if apierrors.IsNotFound(err) || strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, fmt.Errorf("failed to discover %s resources: %w", groupVersion, err)
	}

	for _, res := range resources.APIResources {
		if res.Kind == "Instrumentation" {
			return true, nil
		}
	}
	return false, nil
}
