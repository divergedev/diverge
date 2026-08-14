package deployer

// ServiceStatus reports the deployment state of a single service
// within a preview environment. This is the deployer-agnostic status
// type returned by all Deployer implementations.
type ServiceStatus struct {
	// Name is the unique resource identifier (e.g., ArgoCD Application name
	// or Kubernetes Deployment name).
	Name string
	// Service is the Diverge service name this status belongs to.
	Service string
	// SyncStatus indicates whether the desired state has been applied.
	// Values: "Synced", "OutOfSync", "Applied", "Unknown".
	SyncStatus string
	// Health indicates the runtime health of the deployed service.
	// Values: "Healthy", "Progressing", "Degraded", "Missing",
	// "Current", "InProgress", "Failed", "Terminating".
	Health string
	// URL is an optional endpoint where this service can be reached.
	URL string
	// Message provides additional context about the current status.
	Message string
}
