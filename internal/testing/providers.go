package testing

import "github.com/divergedev/diverge/pkg/registry"

// Providers is the registry of available TestRunner implementations.
var Providers = registry.New[TestRunner]("test-runner")
