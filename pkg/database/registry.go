package database

import "github.com/divergedev/diverge/pkg/registry"

// Providers is the registry of available DatabaseProvider implementations.
var Providers = registry.New[DatabaseProvider]("database")
