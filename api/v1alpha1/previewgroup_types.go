package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PreviewGroupPhase represents the lifecycle state of a PreviewGroup.
type PreviewGroupPhase string

const (
	// PreviewGroupPhasePending indicates the preview group has been created but deployment hasn't started.
	PreviewGroupPhasePending PreviewGroupPhase = "Pending"
	// PreviewGroupPhaseDeploying indicates one or more services in the group are currently deploying.
	PreviewGroupPhaseDeploying PreviewGroupPhase = "Deploying"
	// PreviewGroupPhaseRunning indicates all services in the group are deployed and running successfully.
	PreviewGroupPhaseRunning PreviewGroupPhase = "Running"
	// PreviewGroupPhaseDegraded indicates the group is mostly running but one or more services are failing.
	PreviewGroupPhaseDegraded PreviewGroupPhase = "Degraded"
	// PreviewGroupPhaseFailed indicates the deployment of the preview group has failed critically.
	PreviewGroupPhaseFailed PreviewGroupPhase = "Failed"
	// PreviewGroupPhaseTerminating indicates the preview group is currently being torn down and deleted.
	PreviewGroupPhaseTerminating PreviewGroupPhase = "Terminating"
	// PreviewGroupPhaseAbandoned indicates the preview group was abandoned or orphaned by its creator.
	PreviewGroupPhaseAbandoned PreviewGroupPhase = "Abandoned"
)

// ServiceMode defines how a service participates in a preview group.
type ServiceMode string

const (
	// ServiceModeImage deploys a container image as a preview pod (default).
	ServiceModeImage ServiceMode = "image"
	// ServiceModeLocal routes traffic to an external endpoint (e.g. developer's Tailscale IP)
	// instead of deploying a pod. Enables hot-reload local dev with cloud baselines.
	ServiceModeLocal ServiceMode = "local"
	// ServiceModeBaseline includes the existing baseline service in group routing
	// without deploying a new version.
	ServiceModeBaseline ServiceMode = "baseline"
)

// ServiceProtocol defines the transport protocol for a service.
type ServiceProtocol string

const (
	// ServiceProtocolHTTP is standard HTTP/1.1 or HTTP/2 (including ConnectRPC).
	// Generates HTTPRoute for Gateway API routing.
	ServiceProtocolHTTP ServiceProtocol = "http"
	// ServiceProtocolGRPC is raw gRPC over HTTP/2.
	// Generates GRPCRoute instead of HTTPRoute for Gateway API routing.
	ServiceProtocolGRPC ServiceProtocol = "grpc"
)

// PreviewGroupServiceSpec defines a single service in a preview group.
// Only Name is required; Port, ParentRef, and PathPrefix
// are auto-discovered from existing Kubernetes Services if omitted.
type PreviewGroupServiceSpec struct {
	// Name of the Kubernetes Service to preview. Must match an existing Service.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Image is the container image to deploy for mode=image.
	// Required when mode is "image", ignored for "local" and "baseline".
	// +optional
	Image string `json:"image,omitempty"`

	// Mode defines how this service participates in the preview group.
	// +kubebuilder:validation:Enum=image;local;baseline
	// +kubebuilder:default=image
	// +optional
	Mode ServiceMode `json:"mode,omitempty"`

	// Endpoint is the external endpoint for mode=local
	// (e.g. "100.64.23.42:8080" for a developer's Tailscale IP).
	// Required when mode is "local", ignored otherwise.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// Namespace is the Kubernetes namespace where the service will be deployed. Required. If omitted, falls back to the controller's default namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Port is the container port. Auto-discovered from existing Service if 0.
	// +optional
	Port int32 `json:"port,omitempty"`

	// ParentRef is the Gateway API parentRef name (e.g. the Istio waypoint proxy
	// or a gateway). Auto-discovered if empty.
	// +optional
	ParentRef string `json:"parentRef,omitempty"`

	// PathPrefix scopes HTTPRoute matching to a specific path prefix
	// (e.g. "/api/payments") to avoid shadowing the baseline service.
	// Auto-discovered if empty.
	// +kubebuilder:validation:Pattern=`^/.*$`
	// +optional
	PathPrefix string `json:"pathPrefix,omitempty"`

	// Protocol determines whether to generate an HTTPRoute or GRPCRoute.
	// +kubebuilder:validation:Enum=http;grpc
	// +kubebuilder:default=http
	// +optional
	Protocol ServiceProtocol `json:"protocol,omitempty"`

	// AsyncRoutes defines async routing targets to provision for this service.
	// +optional
	AsyncRoutes []AsyncRouteSpec `json:"asyncRoutes,omitempty"`

	// ImagePullPolicy overrides the container image pull policy.
	// +kubebuilder:validation:Enum=Always;Never;IfNotPresent
	// +optional
	ImagePullPolicy string `json:"imagePullPolicy,omitempty"`

	// Env specifies additional environment variables for the preview pod.
	// +optional
	Env []EnvVar `json:"env,omitempty"`

	// Resources overrides the baseline resource requests/limits for preview pods.
	// If unset, the controller defaults to conservative values (100m CPU, 256Mi RAM).
	// +optional
	Resources *ResourceOverride `json:"resources,omitempty"`

	// Database overrides the group-level database configuration for this service.
	// +optional
	Database *EnvironmentDatabase `json:"database,omitempty"`

	// PostDeploy configures a generic post-deployment Job hook for this service.
	// +optional
	PostDeploy *PostDeploySpec `json:"postDeploy,omitempty"`

	// KEDA configures per-service autoscaling and scale-to-zero settings.
	// Overrides controller-level CLI flags when set.
	// +optional
	KEDA *KEDASpec `json:"keda,omitempty"`
}

// KEDASpec defines per-service autoscaling configuration for KEDA.
type KEDASpec struct {
	// Enabled enables KEDA autoscaling for this service.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
	// MinReplicas is the minimum number of replicas. Set to 0 for scale-to-zero.
	// When nil, falls back to the controller CLI flag (--keda-min-replicas).
	// +kubebuilder:validation:Minimum=0
	// +optional
	MinReplicas *int32 `json:"minReplicas,omitempty"`
	// MaxReplicas is the maximum number of replicas.
	// When nil, falls back to the controller CLI flag (--keda-max-replicas).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`
	// CooldownPeriod is the cooldown period in seconds before scaling down.
	// When nil, falls back to the controller CLI flag (--keda-cooldown).
	// +kubebuilder:validation:Minimum=0
	// +optional
	CooldownPeriod *int32 `json:"cooldownPeriod,omitempty"`
	// PollingInterval is the interval in seconds KEDA checks metrics. Default: 30.
	// +kubebuilder:validation:Minimum=1
	// +optional
	PollingInterval *int32 `json:"pollingInterval,omitempty"`
	// TargetQueueSize is the target backlog per replica for queue-based scaling. Default: 5.
	// +optional
	TargetQueueSize *int32 `json:"targetQueueSize,omitempty"`
}

// ResourceOverride allows preview pods to use fewer resources than baseline.
type ResourceOverride struct {
	// CPURequest is the CPU request for the preview pod (e.g. "100m").
	// +optional
	CPURequest string `json:"cpuRequest,omitempty"`
	// MemoryRequest is the memory request for the preview pod (e.g. "256Mi").
	// +optional
	MemoryRequest string `json:"memoryRequest,omitempty"`
	// CPULimit is the CPU limit for the preview pod.
	// +optional
	CPULimit string `json:"cpuLimit,omitempty"`
	// MemoryLimit is the memory limit for the preview pod.
	// +optional
	MemoryLimit string `json:"memoryLimit,omitempty"`
}

// PreviewGroupRouting configures how traffic reaches preview services.
type PreviewGroupRouting struct {
	// Mode is the routing mode.
	// +kubebuilder:validation:Enum=header;subdomain
	// +kubebuilder:default=header
	// +optional
	Mode string `json:"mode,omitempty"`

	// HeaderKey is the HTTP header key used for routing.
	// Defaults to "x-preview-env".
	// +kubebuilder:default="x-preview-env"
	// +optional
	HeaderKey string `json:"headerKey,omitempty"`

	// HeaderValue is the value that identifies this preview group.
	// Typically set to the MR number (e.g. "42") or branch name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	HeaderValue string `json:"headerValue"`

	// ExternalURL is an optional shareable URL for non-engineer access
	// (e.g. "https://mr-42.preview.dev.azra-ai.com").
	// +optional
	ExternalURL string `json:"externalUrl,omitempty"`

	// BaseDomain is the base domain for subdomain routing.
	// +optional
	BaseDomain string `json:"baseDomain,omitempty"`
}

// PreviewGroupLifecycle configures automatic cleanup and TTL for the group.
type PreviewGroupLifecycle struct {
	// TTL is the time-to-live for the preview group.
	// After this duration, the group is automatically deleted.
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// CleanupOnMerge controls whether to automatically delete the preview group
	// when the source MR/PR is merged or closed.
	// +optional
	CleanupOnMerge bool `json:"cleanupOnMerge,omitempty"`
}

// PreviewGroupSpec defines the desired state of a PreviewGroup.
type PreviewGroupSpec struct {
	// Source identifies the SCM context (GitLab MR, GitHub PR, etc.)
	// +kubebuilder:validation:Required
	Source EnvironmentSource `json:"source"`

	// Routing configures header-based traffic routing shared by all services.
	// +kubebuilder:validation:Required
	Routing PreviewGroupRouting `json:"routing"`

	// Services defines the services included in this preview group.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Services []PreviewGroupServiceSpec `json:"services"`

	// Database provides group-level database configuration.
	// Individual services can override this in their own database field.
	// +optional
	Database *EnvironmentDatabase `json:"database,omitempty"`

	// Lifecycle configures TTL and automatic cleanup.
	// +optional
	Lifecycle *PreviewGroupLifecycle `json:"lifecycle,omitempty"`

	// Owner is the username of the developer who created this PreviewGroup.
	// Used for collision detection and audit.
	Owner string `json:"owner,omitempty"`

	// TopologyOverrides provides inline service dependency edges for environments
	// created via CI/CD or webhooks where .diverge.yaml is not accessible.
	// These override auto-discovered edges from Gateway API and Prometheus.
	// +optional
	TopologyOverrides []TopologyEdge `json:"topologyOverrides,omitempty"`
}

// TopologyEdge represents a directed dependency between two services.
type TopologyEdge struct {
	// From is the source service name (the caller).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	From string `json:"from"`

	// To is the target service name (the callee).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	To string `json:"to"`

	// Protocol is the communication protocol (http, grpc, async).
	// +kubebuilder:validation:Enum=http;grpc;async
	// +kubebuilder:default=http
	Protocol string `json:"protocol,omitempty"`

	// Suppress removes this edge from the auto-discovered graph (tombstone).
	// Use to correct false-positive edges from Prometheus metrics.
	// +optional
	Suppress bool `json:"suppress,omitempty"`
}

// PreviewGroupServiceStatus reports the current state of a single service
// within a PreviewGroup.
type PreviewGroupServiceStatus struct {
	// Name is the service name.
	Name string `json:"name"`

	// EnvironmentName is the name of the child Environment CR created for this service.
	EnvironmentName string `json:"environmentName,omitempty"`

	// Namespace is where the preview pod is deployed.
	Namespace string `json:"namespace,omitempty"`

	// Phase is the current lifecycle phase of this service's preview.
	Phase EnvironmentPhase `json:"phase,omitempty"`

	// URL is the reachable URL for this service's preview.
	URL string `json:"url,omitempty"`

	// Message is a human-readable status message.
	Message string `json:"message,omitempty"`

	// Reason is a machine-readable failure reason
	// (e.g. "CrashLoopBackOff", "ImagePullBackOff", "OOMKilled").
	Reason string `json:"reason,omitempty"`

	// LastLogSnippet contains the last few lines from the preview pod's
	// container logs, enabling quick debugging from the MR comment.
	LastLogSnippet string `json:"lastLogSnippet,omitempty"`

	// ChangedServices lists the services that were modified in this preview,
	// sourced from the child Environment's spec.deploy.changedServices.
	ChangedServices []string `json:"changedServices,omitempty"`
}

// PreviewGroupStatus describes the observed state of a PreviewGroup.
type PreviewGroupStatus struct {
	// Phase is the aggregated phase across all services.
	// +kubebuilder:validation:Enum=Pending;Deploying;Running;Degraded;Failed;Terminating
	Phase PreviewGroupPhase `json:"phase,omitempty"`

	// ServiceCount is the total number of services in the group.
	ServiceCount int32 `json:"serviceCount,omitempty"`

	// Services contains per-service status.
	// +optional
	Services []PreviewGroupServiceStatus `json:"services,omitempty"`

	// ObservedGeneration is the last observed generation of the spec.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// CreatedAt is when the preview group was first created.
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// ExpiresAt is when the preview group will expire (from TTL).
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// Conditions represent the latest available observations of the
	// PreviewGroup's state. Includes "Ready" and "Degraded" conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// CommentID is the ID of the comment tracking the preview group status.
	// +optional
	CommentID int64 `json:"commentID,omitempty"`

	// LeaseRenewedAt is the last time the owning CLI heartbeat renewed the lease.
	LeaseRenewedAt *metav1.Time `json:"leaseRenewedAt,omitempty"`

	// DiscoveredIngressPaths contains the resolved ingress routing paths
	// from gateway entrypoints to the changed services in this group.
	// Populated by the topology discovery engine during environment creation.
	// +optional
	DiscoveredIngressPaths []DiscoveredIngressPath `json:"discoveredIngressPaths,omitempty"`

	// GraphSource describes where the topology graph was discovered from
	// (e.g. "gateway-api+prometheus+static").
	// +optional
	GraphSource string `json:"graphSource,omitempty"`
}

// DiscoveredIngressPath represents a resolved routing path from a gateway
// entrypoint to a target service.
type DiscoveredIngressPath struct {
	// Entrypoint is the gateway service name where the path originates.
	Entrypoint string `json:"entrypoint"`

	// Target is the destination service name.
	Target string `json:"target"`

	// Hops is the ordered list of services along the path,
	// from entrypoint to target inclusive.
	Hops []string `json:"hops"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase",description="Aggregated phase of the preview group"
// +kubebuilder:printcolumn:name="Services",type="integer",JSONPath=".status.serviceCount",description="Number of services"
// +kubebuilder:printcolumn:name="Header",type="string",JSONPath=".spec.routing.headerValue",description="Routing header value"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// PreviewGroup is a collection of related preview services deployed together
// from a single MR/PR. It acts as an "operator of operators", creating and
// managing child Environment CRs across multiple namespaces.
//
// PreviewGroup is cluster-scoped because services in a preview group may span
// multiple namespaces (e.g. product-rad, product-clinical, platform-core).
type PreviewGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PreviewGroupSpec   `json:"spec,omitempty"`
	Status PreviewGroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PreviewGroupList contains a list of PreviewGroup
type PreviewGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PreviewGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PreviewGroup{}, &PreviewGroupList{})
}
