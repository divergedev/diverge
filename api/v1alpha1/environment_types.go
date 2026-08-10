package v1alpha1

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

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
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=gitlab;github
	Provider string `json:"provider"` // e.g., gitlab
	// +kubebuilder:validation:Required
	Project string `json:"project"`
	MR      int    `json:"mr,omitempty"`
	// +kubebuilder:validation:Required
	Branch    string `json:"branch"`
	CommitSHA string `json:"commitSHA,omitempty"`
}

// EnvironmentDeploy defines the deployment configuration
type EnvironmentDeploy struct {
	// +kubebuilder:validation:Enum=delta;full
	Mode string `json:"mode,omitempty"` // delta or full
	// +kubebuilder:validation:Enum=same;create
	// +kubebuilder:default=same
	Namespace string `json:"namespace,omitempty"` // same = deploy in CR's namespace, create = new diverge-* namespace
	// +optional
	NamespaceLabels map[string]string `json:"namespaceLabels,omitempty"`
	ChangedServices []string          `json:"changedServices,omitempty"`
	BaselineRef     string            `json:"baselineRef,omitempty"`
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
	CommitSHA          string             `json:"commitSHA,omitempty"`
	CommentID          int                `json:"commentID,omitempty"`
	CommitStatusURL    string             `json:"commitStatusURL,omitempty"`
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

// PreviewNamespace returns the namespace name used for this environment's
// preview resources when running in "create" namespace mode. The name is
// sanitized to a valid DNS-1123 label (lowercase alphanumeric and hyphens,
// max 63 characters) with a stable hash suffix when truncation is needed.
// The hash incorporates both Name and Namespace to prevent collisions across
// namespaces.
func (e *Environment) PreviewNamespace() string {
	// Sanitize to DNS-1123 label: lowercase, replace dots/underscores with hyphens,
	// strip anything that isn't alphanumeric or hyphen, collapse consecutive hyphens
	raw := strings.ToLower("diverge-" + e.Name)
	raw = strings.NewReplacer(".", "-", "_", "-").Replace(raw)
	raw = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(raw, "")
	raw = regexp.MustCompile(`-{2,}`).ReplaceAllString(raw, "-")
	raw = strings.Trim(raw, "-")

	// Hash includes both name and namespace for cross-namespace uniqueness
	hashInput := e.Namespace + "/" + e.Name
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]

	if len(raw) <= 63-9 {
		// Short enough to append hash without truncation
		return raw + "-" + hash
	}
	// Truncate and append hash
	return raw[:63-9] + "-" + hash
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
