package database

// Re-export types from pkg/database so existing internal consumers
// continue to work without changing their imports.
//
// New code should import "github.com/divergedev/diverge/pkg/database" directly.

import (
	pkgdb "github.com/divergedev/diverge/pkg/database"
)

// Type aliases for backward compatibility with internal consumers.
type DatabaseProvider = pkgdb.DatabaseProvider

// DatabaseResult represents the configuration or state for this type.
type DatabaseResult = pkgdb.DatabaseResult

// DatabaseResult represents the configuration or state for this type.
type DatabaseStatus = pkgdb.DatabaseStatus

// DatabaseStatus represents the configuration or state for this type.
type ProviderConfig = pkgdb.ProviderConfig

// ProviderConfig represents the configuration or state for this type.
type ProviderFactory = pkgdb.ProviderFactory

// ProviderFactory ...

// Re-export registry functions for backward compatibility.
var (
	RegisterProvider    = pkgdb.RegisterProvider
	GetProvider         = pkgdb.GetProvider
	RegisteredProviders = pkgdb.RegisteredProviders
)
