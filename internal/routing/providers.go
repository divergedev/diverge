package routing

import "github.com/divergedev/diverge/pkg/registry"

// Providers is the registry of available Router implementations.
var Providers = registry.New[Router]("router")
