package routing

import (
	"fmt"
	"strings"

	"github.com/divergedev/diverge/pkg/registry"
)

// Providers is the registry of available Router implementations.
var Providers = registry.New[Router]("router")

// NewRouter creates a Router from a provider specification.
// The specification can be a single registered provider name (e.g., "gateway", "istio")
// or a comma-separated list of provider names (e.g., "gateway,istio").
// If multiple providers are specified, they are composed into a CompositeRouter.
// An empty specification defaults to "gateway".
func NewRouter(spec string, deps registry.Deps) (Router, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		spec = "gateway"
	}

	parts := strings.Split(spec, ",")
	validParts := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			validParts = append(validParts, trimmed)
		}
	}

	if len(validParts) == 0 {
		return Providers.Create("gateway", deps)
	}

	if len(validParts) == 1 {
		return Providers.Create(validParts[0], deps)
	}

	routers := make(map[string]Router, len(validParts))
	for _, name := range validParts {
		r, err := Providers.Create(name, deps)
		if err != nil {
			return nil, fmt.Errorf("creating composite sub-router %q: %w", name, err)
		}
		routers[name] = r
	}

	return &CompositeRouter{Routers: routers}, nil
}
