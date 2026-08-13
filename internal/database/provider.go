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
type DatabaseResult = pkgdb.DatabaseResult
type DatabaseStatus = pkgdb.DatabaseStatus
type ProviderConfig = pkgdb.ProviderConfig
type ProviderFactory = pkgdb.ProviderFactory

// Re-export registry functions for backward compatibility.
var (
	RegisterProvider    = pkgdb.RegisterProvider
	GetProvider         = pkgdb.GetProvider
	RegisteredProviders = pkgdb.RegisteredProviders
)
