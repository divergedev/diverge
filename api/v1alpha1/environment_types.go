package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SecretRef struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Key       string `json:"key,omitempty"`
}

type MigrationJobSpec struct {
	Image   string      `json:"image,omitempty"`
	Args    []string    `json:"args,omitempty"`
	EnvFrom []SecretRef `json:"envFrom,omitempty"`
}

// EnvironmentSource defines the source code origin for the environment
type EnvironmentSource struct {
	// +kubebuilder:validation:Enum=gitlab;github
	Provider string `json:"provider,omitempty"` // e.g., gitlab
	Project  string `json:"project,omitempty"`
	MR       int    `json:"mr,omitempty"`
	Branch   string `json:"branch,omitempty"`
}

// EnvironmentDeploy defines the deployment configuration
type EnvironmentDeploy struct {
	// +kubebuilder:validation:Enum=delta;full
	Mode string `json:"mode,omitempty"` // delta or full
	// +kubebuilder:validation:Enum=same;create
	// +kubebuilder:default=same
	Namespace       string   `json:"namespace,omitempty"` // same = deploy in CR's namespace, create = new diverge-* namespace
	ChangedServices []string `json:"changedServices,omitempty"`
	BaselineRef     string   `json:"baselineRef,omitempty"`
}

// EnvironmentRouting defines the routing configuration
type EnvironmentRouting struct {
	// +kubebuilder:validation:Enum=header;namespace;subdomain
	Mode string `json:"mode,omitempty"` // header, namespace, subdomain
	// +kubebuilder:validation:Enum=istio;gateway
	// +kubebuilder:default=gateway
	Provider    string `json:"provider,omitempty"`
	HeaderKey   string `json:"headerKey,omitempty"`
	HeaderValue string `json:"headerValue,omitempty"`
	ExternalURL string `json:"externalUrl,omitempty"`
}

// EnvironmentDatabase defines the database configuration
type EnvironmentDatabase struct {
	// +kubebuilder:validation:Enum=shared;schema;snapshot;fresh
	Mode          string            `json:"mode,omitempty"` // shared, schema, snapshot, fresh
	ConnectionRef string            `json:"connectionRef,omitempty"`
	SeedSource    string            `json:"seedSource,omitempty"`
	MigrationJob  *MigrationJobSpec `json:"migrationJob,omitempty"`
}

// EnvironmentLifecycle defines the lifecycle configuration
type EnvironmentLifecycle struct {
	TTL            *metav1.Duration `json:"ttl,omitempty"`
	CleanupOnMerge bool             `json:"cleanupOnMerge,omitempty"`
}

// EnvironmentSpec defines the desired state of Environment
type EnvironmentSpec struct {
	Source    EnvironmentSource    `json:"source,omitempty"`
	Deploy    EnvironmentDeploy    `json:"deploy,omitempty"`
	Routing   EnvironmentRouting   `json:"routing,omitempty"`
	Database  EnvironmentDatabase  `json:"database,omitempty"`
	Lifecycle EnvironmentLifecycle `json:"lifecycle,omitempty"`
}

// EnvironmentPhase defines the phases an Environment can be in
type EnvironmentPhase string

const (
	PhasePending     EnvironmentPhase = "Pending"
	PhaseDeploying   EnvironmentPhase = "Deploying"
	PhaseRunning     EnvironmentPhase = "Running"
	PhaseFailed      EnvironmentPhase = "Failed"
	PhaseTerminating EnvironmentPhase = "Terminating"
)

// EnvironmentStatus defines the observed state of Environment
type EnvironmentStatus struct {
	Phase              EnvironmentPhase   `json:"phase,omitempty"`
	URL                string             `json:"url,omitempty"`
	Services           []string           `json:"services,omitempty"`
	DatabaseStatus     string             `json:"databaseStatus,omitempty"`
	CreatedAt          *metav1.Time       `json:"createdAt,omitempty"`
	ExpiresAt          *metav1.Time       `json:"expiresAt,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase",description="The current phase of the environment"
// +kubebuilder:printcolumn:name="URL",type="string",JSONPath=".status.url",description="The external URL of the environment"

// Environment is the Schema for the environments API
type Environment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EnvironmentSpec   `json:"spec,omitempty"`
	Status EnvironmentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EnvironmentList contains a list of Environment
type EnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Environment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Environment{}, &EnvironmentList{})
}
