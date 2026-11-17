package multicluster

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

const (
	// LocalClusterName is the canonical cluster identifier for workloads deployed on the hub cluster.
	LocalClusterName = "local"

	defaultMaxCachedClusters = 128
	defaultRequestTimeout    = 15 * time.Second
)

// ValidationError indicates an invalid or forbidden clusterRef configuration that
// should not be retried without a spec or Secret update.
type ValidationError struct {
	Reason  string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// IsValidationError reports whether err is (or wraps) a *ValidationError.
func IsValidationError(err error) (*ValidationError, bool) {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve, true
	}
	return nil, false
}

// Target holds resolved clients and metadata for a hub or spoke Kubernetes cluster.
type Target struct {
	Name               string
	IsRemote           bool
	Client             client.Client
	Clientset          kubernetes.Interface
	StreamingClientset kubernetes.Interface
	RESTConfig         *rest.Config
}

type targetContextKey struct{}

// WithTarget attaches a pre-resolved Target to ctx so composite operations (such as
// KEDADeployer wrapping DirectDeployer) use the exact same cluster client throughout.
func WithTarget(ctx context.Context, target *Target) context.Context {
	if target == nil {
		return ctx
	}
	return context.WithValue(ctx, targetContextKey{}, target)
}

// TargetFromContext returns a pre-resolved Target attached via WithTarget, if present.
func TargetFromContext(ctx context.Context) (*Target, bool) {
	if ctx == nil {
		return nil, false
	}
	t, ok := ctx.Value(targetContextKey{}).(*Target)
	return t, ok && t != nil
}

// Resolver resolves hub or spoke cluster clients for a given Environment.
type Resolver interface {
	Resolve(ctx context.Context, env *v1alpha1.Environment) (*Target, error)
}

// ClientFactory constructs controller-runtime and client-go clients from a sanitized REST config.
type ClientFactory func(cfg *rest.Config, scheme *runtime.Scheme) (client.Client, kubernetes.Interface, error)

type cachedCluster struct {
	hash               [32]byte
	client             client.Client
	clientset          kubernetes.Interface
	streamingClientset kubernetes.Interface
	restConfig         *rest.Config
	createdAt          time.Time
}

// Manager resolves and caches spoke-cluster Kubernetes clients from same-namespace Secrets.
type Manager struct {
	HubClient    client.Client
	HubClientset kubernetes.Interface
	HubConfig    *rest.Config
	Scheme       *runtime.Scheme

	// AllowLoopbackForTesting permits 127.0.0.1 / ::1 / localhost API server endpoints in tests.
	AllowLoopbackForTesting bool
	// AllowInsecureHTTPForTesting permits http:// API server endpoints in unit tests.
	AllowInsecureHTTPForTesting bool
	// ClientFactory overrides client construction when non-nil (primarily for testing).
	ClientFactory ClientFactory
	// LookupIPAddr overrides DNS resolution in connection-time dial guards when non-nil (primarily for testing).
	LookupIPAddr func(ctx context.Context, host string) ([]net.IPAddr, error)
	// MaxCacheEntries bounds the number of cached spoke clients (defaults to 128).
	MaxCacheEntries int

	mu    sync.RWMutex
	cache map[string]cachedCluster
}

// NewManager creates a multi-cluster client manager backed by the hub cluster client.
func NewManager(hubClient client.Client, hubClientset kubernetes.Interface, hubCfg *rest.Config, scheme *runtime.Scheme) *Manager {
	return &Manager{
		HubClient:       hubClient,
		HubClientset:    hubClientset,
		HubConfig:       hubCfg,
		Scheme:          scheme,
		MaxCacheEntries: defaultMaxCachedClusters,
		cache:           make(map[string]cachedCluster),
	}
}

// ClusterName returns the logical target cluster name for an Environment ("local" when unset).
func ClusterName(env *v1alpha1.Environment) string {
	if env == nil {
		return LocalClusterName
	}
	if s := strings.TrimSpace(env.Status.TargetCluster); s != "" {
		return s
	}
	return DesiredClusterName(env.Spec.Deploy.ClusterRef)
}

// DesiredClusterName derives the logical target cluster name from a ClusterTargetRef.
func DesiredClusterName(ref *v1alpha1.ClusterTargetRef) string {
	if ref == nil {
		return LocalClusterName
	}
	if name := strings.TrimSpace(ref.Name); name != "" {
		return name
	}
	if ref.KubeconfigSecretRef != nil {
		if secName := strings.TrimSpace(ref.KubeconfigSecretRef.Name); secName != "" {
			return secName
		}
	}
	return LocalClusterName
}

// IsRemoteEnv reports whether env targets a remote spoke cluster rather than the local hub.
func IsRemoteEnv(env *v1alpha1.Environment) bool {
	if env == nil || env.Spec.Deploy.ClusterRef == nil {
		return false
	}
	ref := env.Spec.Deploy.ClusterRef
	if ref.KubeconfigSecretRef != nil {
		return true
	}
	name := strings.TrimSpace(ref.Name)
	return name != "" && name != LocalClusterName
}

// HasConfirmedRemoteTarget reports whether env has already been committed to a remote spoke cluster
// in Status.TargetCluster, meaning teardown must not silently skip spoke cleanup on kubeconfig validation errors.
func HasConfirmedRemoteTarget(env *v1alpha1.Environment) bool {
	if env == nil {
		return false
	}
	tc := strings.TrimSpace(env.Status.TargetCluster)
	return tc != "" && tc != LocalClusterName
}

// Resolve returns the hub or spoke Target for env.
func (m *Manager) Resolve(ctx context.Context, env *v1alpha1.Environment) (*Target, error) {
	if pinned, ok := TargetFromContext(ctx); ok {
		return pinned, nil
	}
	if m == nil {
		return nil, fmt.Errorf("multicluster manager is nil")
	}
	if env == nil || !IsRemoteEnv(env) {
		return &Target{
			Name:               LocalClusterName,
			IsRemote:           false,
			Client:             m.HubClient,
			Clientset:          m.HubClientset,
			StreamingClientset: m.HubClientset,
			RESTConfig:         m.HubConfig,
		}, nil
	}

	clusterRef := env.Spec.Deploy.ClusterRef
	if clusterRef.KubeconfigSecretRef == nil {
		return nil, &ValidationError{
			Reason:  "MissingKubeconfigSecretRef",
			Message: fmt.Sprintf("clusterRef %q must specify kubeconfigSecretRef", clusterRef.Name),
		}
	}

	secRef := clusterRef.KubeconfigSecretRef
	if secNS := strings.TrimSpace(secRef.Namespace); secNS != "" && secNS != env.Namespace {
		return nil, &ValidationError{
			Reason: "CrossNamespaceRef",
			Message: fmt.Sprintf(
				"cross-namespace kubeconfig secret reference %q/%q is forbidden; secret must reside in environment namespace %q",
				secNS, secRef.Name, env.Namespace,
			),
		}
	}

	secretName := strings.TrimSpace(secRef.Name)
	if secretName == "" {
		return nil, &ValidationError{
			Reason:  "InvalidClusterRef",
			Message: "clusterRef.kubeconfigSecretRef.name must not be empty",
		}
	}

	if m.HubClient == nil {
		return nil, fmt.Errorf("hub client is not configured on multicluster manager")
	}

	var secret corev1.Secret
	if err := m.HubClient.Get(ctx, types.NamespacedName{Namespace: env.Namespace, Name: secretName}, &secret); err != nil {
		return nil, fmt.Errorf("fetching spoke kubeconfig secret %s/%s: %w", env.Namespace, secretName, err)
	}

	kubeconfigBytes, resolvedKey, err := extractKubeconfigBytes(&secret, secRef.Key)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(kubeconfigBytes)
	cacheKey := env.Namespace + "/" + secretName + ":" + resolvedKey
	clusterName := DesiredClusterName(clusterRef)

	m.mu.RLock()
	if hit, ok := m.cache[cacheKey]; ok && hit.hash == sum {
		m.mu.RUnlock()
		return &Target{
			Name:               clusterName,
			IsRemote:           true,
			Client:             hit.client,
			Clientset:          hit.clientset,
			StreamingClientset: hit.streamingClientset,
			RESTConfig:         rest.CopyConfig(hit.restConfig),
		}, nil
	}
	m.mu.RUnlock()

	restCfg, err := m.parseAndValidateKubeconfig(kubeconfigBytes)
	if err != nil {
		return nil, err
	}

	factory := m.ClientFactory
	if factory == nil {
		factory = defaultClientFactory
	}

	scheme := m.Scheme
	if scheme == nil && m.HubClient != nil {
		scheme = m.HubClient.Scheme()
	}

	spokeClient, spokeClientset, err := factory(restCfg, scheme)
	if err != nil {
		return nil, fmt.Errorf("constructing spoke client for cluster %q: %w", clusterName, err)
	}

	streamingClientset := spokeClientset
	if m.ClientFactory == nil {
		streamCfg := rest.CopyConfig(restCfg)
		streamCfg.Timeout = 0
		streamingCS, streamErr := kubernetes.NewForConfig(streamCfg)
		if streamErr != nil {
			return nil, fmt.Errorf("constructing spoke streaming clientset for cluster %q: %w", clusterName, streamErr)
		}
		streamingClientset = streamingCS
	}

	m.storeCache(cacheKey, cachedCluster{
		hash:               sum,
		client:             spokeClient,
		clientset:          spokeClientset,
		streamingClientset: streamingClientset,
		restConfig:         rest.CopyConfig(restCfg),
		createdAt:          time.Now(),
	})

	return &Target{
		Name:               clusterName,
		IsRemote:           true,
		Client:             spokeClient,
		Clientset:          spokeClientset,
		StreamingClientset: streamingClientset,
		RESTConfig:         rest.CopyConfig(restCfg),
	}, nil
}

func defaultClientFactory(cfg *rest.Config, scheme *runtime.Scheme) (client.Client, kubernetes.Interface, error) {
	crClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, nil, err
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	return crClient, clientset, nil
}

func extractKubeconfigBytes(secret *corev1.Secret, explicitKey string) ([]byte, string, error) {
	if len(secret.Data) == 0 {
		return nil, "", &ValidationError{
			Reason:  "EmptyKubeconfigSecret",
			Message: fmt.Sprintf("kubeconfig secret %s/%s has no data entries", secret.Namespace, secret.Name),
		}
	}

	if k := strings.TrimSpace(explicitKey); k != "" {
		raw, ok := secret.Data[k]
		if !ok || len(strings.TrimSpace(string(raw))) == 0 {
			return nil, "", &ValidationError{
				Reason:  "MissingKubeconfigKey",
				Message: fmt.Sprintf("kubeconfig secret %s/%s does not contain non-empty key %q", secret.Namespace, secret.Name, k),
			}
		}
		return raw, k, nil
	}

	for _, candidate := range []string{"kubeconfig", "value", "config"} {
		if raw, ok := secret.Data[candidate]; ok && len(strings.TrimSpace(string(raw))) > 0 {
			return raw, candidate, nil
		}
	}

	return nil, "", &ValidationError{
		Reason:  "MissingKubeconfigKey",
		Message: fmt.Sprintf("kubeconfig secret %s/%s must contain key \"kubeconfig\", \"value\", or \"config\" (or set kubeconfigSecretRef.key)", secret.Namespace, secret.Name),
	}
}

func (m *Manager) parseAndValidateKubeconfig(raw []byte) (*rest.Config, error) {
	apiCfg, err := clientcmd.Load(raw)
	if err != nil {
		return nil, &ValidationError{
			Reason:  "InvalidKubeconfig",
			Message: fmt.Sprintf("invalid spoke kubeconfig YAML: %v", err),
		}
	}

	if err := validateClientCmdConfig(apiCfg); err != nil {
		return nil, err
	}

	restCfg, err := clientcmd.NewDefaultClientConfig(*apiCfg, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, &ValidationError{
			Reason:  "InvalidKubeconfig",
			Message: fmt.Sprintf("building spoke REST config: %v", err),
		}
	}

	// Defense-in-depth: scrub any plugin hooks or file-based credentials on the resulting rest.Config.
	restCfg.ExecProvider = nil
	restCfg.AuthProvider = nil
	restCfg.BearerTokenFile = ""
	restCfg.CertFile = ""
	restCfg.KeyFile = ""
	restCfg.CAFile = ""
	if restCfg.Timeout == 0 {
		restCfg.Timeout = defaultRequestTimeout
	}
	if restCfg.QPS == 0 {
		restCfg.QPS = 20
	}
	if restCfg.Burst == 0 {
		restCfg.Burst = 40
	}

	if err := m.validateAPIServerURL(restCfg.Host); err != nil {
		return nil, err
	}
	if restCfg.Dial == nil {
		restCfg.Dial = m.buildSafeDialContext()
	}

	return restCfg, nil
}

func validateClientCmdConfig(cfg *clientcmdapi.Config) error {
	if cfg == nil {
		return &ValidationError{
			Reason:  "InvalidKubeconfig",
			Message: "kubeconfig is empty",
		}
	}
	for name, cluster := range cfg.Clusters {
		if cluster == nil {
			continue
		}
		if strings.TrimSpace(cluster.CertificateAuthority) != "" {
			return &ValidationError{
				Reason:  "ForbiddenLocalFileRef",
				Message: fmt.Sprintf("kubeconfig cluster %q uses certificate-authority file path; use inline certificate-authority-data instead", name),
			}
		}
		if strings.TrimSpace(cluster.ProxyURL) != "" {
			return &ValidationError{
				Reason:  "ForbiddenProxyURL",
				Message: fmt.Sprintf("kubeconfig cluster %q sets proxy-url, which is forbidden", name),
			}
		}
	}

	for name, auth := range cfg.AuthInfos {
		if auth == nil {
			continue
		}
		if auth.Exec != nil {
			return &ValidationError{
				Reason:  "ForbiddenAuthPlugin",
				Message: fmt.Sprintf("kubeconfig user %q uses exec credential plugin, which is forbidden", name),
			}
		}
		if auth.AuthProvider != nil {
			return &ValidationError{
				Reason:  "ForbiddenAuthPlugin",
				Message: fmt.Sprintf("kubeconfig user %q uses auth-provider plugin, which is forbidden", name),
			}
		}
		if strings.TrimSpace(auth.ClientCertificate) != "" ||
			strings.TrimSpace(auth.ClientKey) != "" ||
			strings.TrimSpace(auth.TokenFile) != "" {
			return &ValidationError{
				Reason:  "ForbiddenLocalFileRef",
				Message: fmt.Sprintf("kubeconfig user %q references local credential files; use inline client-certificate-data/client-key-data or token instead", name),
			}
		}
	}

	return nil
}

func (m *Manager) validateAPIServerURL(rawHost string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawHost))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return &ValidationError{
			Reason:  "InvalidAPIServerURL",
			Message: fmt.Sprintf("spoke kubeconfig server URL %q is invalid", rawHost),
		}
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && (scheme != "http" || !m.AllowInsecureHTTPForTesting) {
		return &ValidationError{
			Reason:  "InsecureAPIServerURL",
			Message: fmt.Sprintf("spoke kubeconfig server URL %q must use https://", rawHost),
		}
	}

	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return &ValidationError{
			Reason:  "InvalidAPIServerURL",
			Message: "spoke kubeconfig server hostname is empty",
		}
	}

	if ip := net.ParseIP(hostname); ip != nil {
		return m.validateEndpointIP(ip, hostname)
	}

	if err := m.validateHostname(hostname); err != nil {
		return err
	}

	return nil
}

func (m *Manager) validateHostname(hostname string) error {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "metadata" || hostname == "metadata.google.internal" || strings.HasSuffix(hostname, ".metadata.google.internal") {
		return &ValidationError{
			Reason:  "ForbiddenAPIServerHost",
			Message: fmt.Sprintf("spoke kubeconfig server host %q targets cloud metadata service", hostname),
		}
	}
	if !m.AllowLoopbackForTesting && (hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") || hostname == "kubernetes.default" || hostname == "kubernetes.default.svc" || strings.HasPrefix(hostname, "kubernetes.default.svc.")) {
		return &ValidationError{
			Reason:  "ForbiddenAPIServerHost",
			Message: fmt.Sprintf("spoke kubeconfig server host %q targets local or in-cluster API server", hostname),
		}
	}
	return nil
}

func (m *Manager) validateEndpointIP(ip net.IP, hostLabel string) error {
	if ip == nil {
		return &ValidationError{
			Reason:  "InvalidAPIServerURL",
			Message: fmt.Sprintf("spoke kubeconfig server IP %q is invalid", hostLabel),
		}
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return &ValidationError{
			Reason:  "ForbiddenAPIServerHost",
			Message: fmt.Sprintf("spoke kubeconfig server %q resolves to link-local or unspecified address %s", hostLabel, ip.String()),
		}
	}
	if ip.IsLoopback() && !m.AllowLoopbackForTesting {
		return &ValidationError{
			Reason:  "ForbiddenAPIServerHost",
			Message: fmt.Sprintf("spoke kubeconfig server %q resolves to loopback address %s", hostLabel, ip.String()),
		}
	}
	return nil
}

func (m *Manager) buildSafeDialContext() func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid spoke API server dial address %q: %w", addr, err)
		}
		trimmedHost := strings.TrimSpace(host)
		if literalIP := net.ParseIP(trimmedHost); literalIP != nil {
			if err := m.validateEndpointIP(literalIP, trimmedHost); err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(literalIP.String(), port))
		}
		if err := m.validateHostname(trimmedHost); err != nil {
			return nil, err
		}

		lookup := m.LookupIPAddr
		if lookup == nil {
			lookup = net.DefaultResolver.LookupIPAddr
		}
		addrs, err := lookup(ctx, trimmedHost)
		if err != nil {
			return nil, fmt.Errorf("resolving spoke API server host %q: %w", trimmedHost, err)
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("spoke API server host %q resolved to no IP addresses", trimmedHost)
		}
		for _, candidate := range addrs {
			if err := m.validateEndpointIP(candidate.IP, trimmedHost); err != nil {
				return nil, err
			}
		}

		var lastErr error
		for _, candidate := range addrs {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
	}
}

func (m *Manager) storeCache(key string, entry cachedCluster) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cache == nil {
		m.cache = make(map[string]cachedCluster)
	}
	maxEntries := m.MaxCacheEntries
	if maxEntries <= 0 {
		maxEntries = defaultMaxCachedClusters
	}
	if _, exists := m.cache[key]; !exists && len(m.cache) >= maxEntries {
		var oldestKey string
		var oldestTime time.Time
		for k, v := range m.cache {
			if oldestKey == "" || v.createdAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.createdAt
			}
		}
		if oldestKey != "" {
			delete(m.cache, oldestKey)
		}
	}
	m.cache[key] = entry
}

// StaticResolver is a deterministic in-memory Resolver used in unit tests.
type StaticResolver struct {
	Hub    *Target
	Spokes map[string]*Target
}

// Resolve returns Hub for local environments and Spokes[clusterName] for federated environments.
func (s *StaticResolver) Resolve(ctx context.Context, env *v1alpha1.Environment) (*Target, error) {
	if pinned, ok := TargetFromContext(ctx); ok {
		return pinned, nil
	}
	if env == nil || !IsRemoteEnv(env) {
		if s.Hub != nil {
			return s.Hub, nil
		}
		return nil, fmt.Errorf("hub target not configured")
	}
	if secRef := env.Spec.Deploy.ClusterRef.KubeconfigSecretRef; secRef != nil {
		if ns := strings.TrimSpace(secRef.Namespace); ns != "" && ns != env.Namespace {
			return nil, &ValidationError{
				Reason:  "CrossNamespaceRef",
				Message: fmt.Sprintf("cross-namespace kubeconfig secret reference %q/%q is forbidden", ns, secRef.Name),
			}
		}
	}
	name := DesiredClusterName(env.Spec.Deploy.ClusterRef)
	if t, ok := s.Spokes[name]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("spoke cluster %q not registered in StaticResolver", name)
}
