package features

import "github.com/divergedev/diverge/pkg/registry"

// Providers is the registry of available FeatureProvider implementations.
var Providers = registry.New[FeatureProvider]("feature")
