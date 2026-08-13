package database

import (
	"fmt"
	"sync"
)

// ProviderConfig holds configuration for creating a database provider.
type ProviderConfig struct {
	// AdminDSN is the connection string for administrative database operations.
	AdminDSN string

	// Extra holds provider-specific configuration (e.g., Neon API key, Atlas config).
	Extra map[string]string
}

// ProviderFactory creates a DatabaseProvider from the given configuration.
type ProviderFactory func(cfg ProviderConfig) (DatabaseProvider, error)

var (
	registryMu sync.RWMutex
	registry   = make(map[string]ProviderFactory)
)

// RegisterProvider registers a named database provider factory.
// This is typically called from init() functions in provider packages.
//
// Example:
//
//	func init() {
//	    database.RegisterProvider("neon", func(cfg database.ProviderConfig) (database.DatabaseProvider, error) {
//	        return neon.NewProvider(cfg.Extra["api_key"], cfg.Extra["project_id"])
//	    })
//	}
func RegisterProvider(name string, factory ProviderFactory) {
	if factory == nil {
		panic("database provider factory must not be nil")
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("database provider %q already registered", name))
	}
	registry[name] = factory
}

// GetProvider looks up a registered provider factory by name.
// Returns the factory and true if found, nil and false otherwise.
func GetProvider(name string) (ProviderFactory, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[name]
	return f, ok
}

// RegisteredProviders returns the names of all registered providers.
func RegisteredProviders() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// ResetRegistry clears all registered providers. For testing only.
func ResetRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[string]ProviderFactory)
}
