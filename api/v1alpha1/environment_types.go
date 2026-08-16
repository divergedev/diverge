package v1alpha1

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretRef references a specific key within a Kubernetes Secret.
type SecretRef struct {
	// Namespace is the namespace of the Secret.
	Namespace string `json:"namespace,omitempty"`
	// Name is the name of the Secret.
	Name string `json:"name,omitempty"`
	// Key is the specific key in the Secret to reference.
	Key string `json:"key,omitempty"`
}

// MigrationJobSpec configures a Kubernetes Job for database migrations.
type MigrationJobSpec struct {
	// Image is the container image to use for the migration job.
	Image string `json:"image,omitempty"`
	// Args specifies the arguments to pass to the migration job container.
	Args []string `json:"args,omitempty"`
	// EnvFrom specifies environment variables to inject from Secrets.
	EnvFrom []SecretRef `json:"envFrom,omitempty"`
	// TimeoutSeconds is the maximum duration the migration job can run before being terminated.
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"` // Default 120
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
	// Manifests specifies where pre-rendered manifests are stored.
	// Required when using the direct deployer.
	// +optional
	Manifests *ManifestSource `json:"manifests,omitempty"`
}

// AsyncRouteSpec defines an async routing target for event-driven services.
type AsyncRouteSpec struct {
	// Protocol is the async protocol type.
	// +kubebuilder:validation:Enum=temporal;kafka
	Protocol string `json:"protocol"`

	// Target is the baseline resource name (e.g., task queue name or topic name).
	Target string `json:"target"`

	// EnvVarMapping maps provisioned targets to environment variable names.
	// If empty, sensible defaults are used based on protocol:
	// - temporal: TEMPORAL_TASK_QUEUE
	// - kafka: KAFKA_CONSUMER_GROUP
	// +optional
	EnvVarMapping map[string]string `json:"envVarMapping,omitempty"`
}

// DefaultEnvVarForProtocol returns the default environment variable name for a protocol.
func DefaultEnvVarForProtocol(protocol string) string {
	switch protocol {
	case "temporal":
		return "TEMPORAL_TASK_QUEUE"
	case "kafka":
		return "KAFKA_CONSUMER_GROUP"
	default:
		return ""
	}
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
	DevIP       string `json:"devIP,omitempty"`
	// BaseDomain is the base domain for subdomain routing (e.g., "preview.app.dev").
	// When mode is "subdomain", HTTPRoutes are created with hostname "<env-name>.<baseDomain>".
	// +optional
	BaseDomain string `json:"baseDomain,omitempty"`
	// AsyncRoutes defines async routing targets for event-driven backends.
	// +optional
	AsyncRoutes []AsyncRouteSpec `json:"asyncRoutes,omitempty"`
	// Cookie enables sticky routing using cookies.
	// +optional
	Cookie *CookieSpec `json:"cookie,omitempty"`
}

// CookieSpec defines the configuration for sticky routing cookies.
type CookieSpec struct {
	Enabled bool `json:"enabled,omitempty"`
	// +kubebuilder:validation:Minimum=0
	MaxAge int `json:"maxAge,omitempty"` // seconds, default 86400 (24h)
	// +kubebuilder:validation:Enum=Lax;Strict;None
	SameSite string `json:"sameSite,omitempty"` // Lax, Strict, None
	Secure   bool   `json:"secure,omitempty"`
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

// ManifestSource specifies how pre-rendered manifests are provided
// to the DirectDeployer.
type ManifestSource struct {
	// Type is the manifest source type: "configmap" or "url".
	// +kubebuilder:validation:Enum=configmap;url
	Type string `json:"type"`
	// URL for HTTP-based manifest fetching (when type=url).
	URL string `json:"url,omitempty"`
}

// TestTriggerType defines how tests are triggered.
type TestTriggerType string

const (
	// TestTriggerGitLabPipeline triggers an external GitLab pipeline to run tests.
	TestTriggerGitLabPipeline TestTriggerType = "gitlab-pipeline"
	// TestTriggerGitHubDispatch triggers a GitHub repository dispatch event to run tests.
	TestTriggerGitHubDispatch TestTriggerType = "github-dispatch"
	// TestTriggerWebhook POSTs to an external webhook URL to run tests.
	TestTriggerWebhook TestTriggerType = "webhook"
)

// TestTriggerSpec defines how to trigger a test run.
type TestTriggerSpec struct {
	// +kubebuilder:validation:Enum=gitlab-pipeline;github-dispatch;webhook
	Type TestTriggerType `json:"type"`
	// Project is the CI project to trigger (e.g., "team/e2e-tests").
	// Used by gitlab-pipeline and github-dispatch triggers.
	Project string `json:"project,omitempty"`
	// Ref is the git ref to trigger tests against (e.g., "main").
	Ref string `json:"ref,omitempty"`
	// EventType is the GitHub repository dispatch event type.
	// Used by github-dispatch trigger only.
	EventType string `json:"eventType,omitempty"`
	// WebhookURL is the URL to POST to for webhook triggers.
	WebhookURL string `json:"webhookURL,omitempty"`
	// SecretRef references a Kubernetes Secret containing the trigger token.
	SecretRef string `json:"secretRef,omitempty"`
}

// TestingSpec defines the test integration configuration.
type TestingSpec struct {
	// Enabled controls whether tests are triggered for this environment.
	Enabled bool `json:"enabled,omitempty"`
	// Trigger defines how tests are triggered.
	Trigger TestTriggerSpec `json:"trigger"`
	// Timeout is how long to wait for test results before timing out.
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`
	// Required controls whether test results block the diverge/tests commit status.
	Required bool `json:"required,omitempty"`
}

// TestState represents the current state of a test run.
type TestState string

const (
	// TestStatePending indicates the test run is queued or starting.
	TestStatePending TestState = "pending"
	// TestStateRunning indicates the test run is currently executing.
	TestStateRunning TestState = "running"
	// TestStatePassed indicates the test run completed successfully.
	TestStatePassed TestState = "passed"
	// TestStateFailed indicates the test run completed with errors or test failures.
	TestStateFailed TestState = "failed"
	// TestStateTimedOut indicates the test run did not complete within the configured timeout.
	TestStateTimedOut TestState = "timed-out"
)

// TestStatus represents the observed state of a test run.
type TestStatus struct {
	State       TestState    `json:"state,omitempty"`
	Summary     string       `json:"summary,omitempty"`
	URL         string       `json:"url,omitempty"`
	RunID       string       `json:"runID,omitempty"`
	StartedAt   *metav1.Time `json:"startedAt,omitempty"`
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// ServicePreviewConfig defines how preview pods are deployed for multi-repo services,
// read from the .diverge.yaml file in the service repository.
type ServicePreviewConfig struct {
	// ServiceName is the Kubernetes Service name to shadow.
	ServiceName string `json:"serviceName"`
	// Namespace is the namespace where the baseline service runs.
	Namespace string `json:"namespace,omitempty"`
	// Port is the container port the service listens on.
	Port int32 `json:"port"`
	// Image is the container image for the preview pod (set by CI).
	Image string `json:"image"`
	// ImagePullPolicy overrides the container image pull policy.
	// Valid values: Always, Never, IfNotPresent. Defaults to IfNotPresent.
	ImagePullPolicy string `json:"imagePullPolicy,omitempty"`
	// DatabaseEnvKey is the environment variable name for the database connection URL.
	// Defaults to DATABASE_URL.
	DatabaseEnvKey string `json:"databaseEnvKey,omitempty"`
	// ParentRef is the Gateway API parentRef name (e.g., Istio Waypoint proxy).
	// If empty, defaults to "diverge-gateway".
	ParentRef string `json:"parentRef,omitempty"`
	// HeaderKey overrides the routing header key.
	HeaderKey string `json:"headerKey,omitempty"`
	// PathPrefix scopes HTTPRoute matching to a specific path prefix (e.g., "/api/payments") to avoid shadowing the baseline.
	// +kubebuilder:validation:Pattern=`^/.*$`
	// +optional
	PathPrefix string `json:"pathPrefix,omitempty"`
	// Env is additional environment variables for the preview container.
	Env []EnvVar `json:"env,omitempty"`
	// Protocol specifies the service protocol for routing (http or grpc).
	// When set to "grpc", the router generates a GRPCRoute instead of HTTPRoute.
	// +kubebuilder:validation:Enum=http;grpc
	// +kubebuilder:default=http
	// +optional
	Protocol string `json:"protocol,omitempty"`
	// Endpoint is the external endpoint for mode=local (e.g. Tailscale IP:port).
	// +optional
	Endpoint string `json:"endpoint,omitempty"`
	// Resources overrides the resource requests/limits for the preview pod.
	// +optional
	Resources *ResourceOverride `json:"resources,omitempty"`
	// WebSocket configures WebSocket proxy support.
	// +optional
	WebSocket *WebSocketSpec `json:"websocket,omitempty"`
}

// WebSocketSpec defines WebSocket proxy configuration.
type WebSocketSpec struct {
	Enabled bool   `json:"enabled,omitempty"`
	Path    string `json:"path,omitempty"`    // e.g., "/ws", default: all paths
	Timeout string `json:"timeout,omitempty"` // e.g., "3600s", default: no timeout
}

// EnvVar represents an environment variable for a preview container.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// EnvironmentSpec defines the desired state of Environment
type EnvironmentSpec struct {
	Source    EnvironmentSource    `json:"source,omitempty"`
	Deploy    EnvironmentDeploy    `json:"deploy,omitempty"`
	Routing   EnvironmentRouting   `json:"routing,omitempty"`
	Database  EnvironmentDatabase  `json:"database,omitempty"`
	Lifecycle EnvironmentLifecycle `json:"lifecycle,omitempty"`
	// Testing configures automated test integration.
	// +optional
	Testing *TestingSpec `json:"testing,omitempty"`
	// ServiceConfig holds preview configuration for multi-repo mode.
	// When set, the controller deploys a single preview pod based on
	// this config rather than using ManifestFetcher.
	// +optional
	ServiceConfig *ServicePreviewConfig `json:"serviceConfig,omitempty"`
}

// EnvironmentPhase defines the phases an Environment can be in
type EnvironmentPhase string

const (
	// PhasePending ...
	PhasePending EnvironmentPhase = "Pending"
	// PhaseDeploying ...
	PhaseDeploying EnvironmentPhase = "Deploying"
	// PhaseRunning ...
	PhaseRunning EnvironmentPhase = "Running"
	// PhaseFailed ...
	PhaseFailed EnvironmentPhase = "Failed"
	// PhaseTerminating ...
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
	// TestStatus tracks the state of the test run for this environment.
	// +optional
	TestStatus *TestStatus `json:"testStatus,omitempty"`
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
