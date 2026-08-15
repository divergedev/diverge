package deployer

import "github.com/divergedev/diverge/pkg/registry"

// Providers is the registry of available Deployer implementations.
var Providers = registry.New[Deployer]("deployer")
