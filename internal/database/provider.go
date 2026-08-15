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

// DatabaseStatus aliases the public DatabaseStatus type...
type DatabaseStatus = pkgdb.DatabaseStatus

// ProviderConfig aliases the public ProviderConfig type...
type ProviderConfig = pkgdb.ProviderConfig

// ProviderFactory aliases the public ProviderFactory type...
type ProviderFactory = pkgdb.ProviderFactory

// Re-export registry functions for backward compatibility.
var (
	RegisterProvider    = pkgdb.RegisterProvider
	GetProvider         = pkgdb.GetProvider
	RegisteredProviders = pkgdb.RegisteredProviders
)
