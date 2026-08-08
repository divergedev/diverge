package database

// DatabaseProvider defines the interface for database provisioning
type DatabaseProvider interface {
	Provision() error
	Teardown() error
	ConnectionString() string
	Status() string
}

// SharedProvider provides a shared database connection
type SharedProvider struct{}
func (p *SharedProvider) Provision() error { return nil }
func (p *SharedProvider) Teardown() error { return nil }
func (p *SharedProvider) ConnectionString() string { return "" }
func (p *SharedProvider) Status() string { return "Ready" }

// SchemaProvider creates a new logical schema within an existing database
type SchemaProvider struct{}
func (p *SchemaProvider) Provision() error { return nil }
func (p *SchemaProvider) Teardown() error { return nil }
func (p *SchemaProvider) ConnectionString() string { return "" }
func (p *SchemaProvider) Status() string { return "Ready" }

// FreshProvider provisions a completely new database instance
type FreshProvider struct{}
func (p *FreshProvider) Provision() error { return nil }
func (p *FreshProvider) Teardown() error { return nil }
func (p *FreshProvider) ConnectionString() string { return "" }
func (p *FreshProvider) Status() string { return "Ready" }

// SnapshotProvider provisions a database from a snapshot
type SnapshotProvider struct{}
func (p *SnapshotProvider) Provision() error { return nil }
func (p *SnapshotProvider) Teardown() error { return nil }
func (p *SnapshotProvider) ConnectionString() string { return "" }
func (p *SnapshotProvider) Status() string { return "Ready" }
