package features

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	Providers.Register("unleash", registry.Provider[FeatureProvider]{
		Create: func(deps registry.Deps) (FeatureProvider, error) {
			return NewUnleashProvider(deps.Client, deps.Scheme, deps.Logger), nil
		},
		Description: "Unleash enterprise feature flag provider (context-constrained activation strategies & variants)",
	})
}

// UnleashProvider implements FeatureProvider for Unleash feature management.
type UnleashProvider struct {
	client     client.Client
	scheme     *runtime.Scheme
	logger     logr.Logger
	httpClient *http.Client
}

// NewUnleashProvider creates a new UnleashProvider.
func NewUnleashProvider(c client.Client, scheme *runtime.Scheme, logger logr.Logger) *UnleashProvider {
	return &UnleashProvider{
		client: c,
		scheme: scheme,
		logger: logger,
	}
}

// WithHTTPClient allows overriding the default HTTP client (primarily for testing).
func (p *UnleashProvider) WithHTTPClient(httpClient *http.Client) *UnleashProvider {
	p.httpClient = httpClient
	return p
}

// Type returns "unleash".
func (p *UnleashProvider) Type() string {
	return "unleash"
}

// UnleashStrategyTitle generates a deterministic RFC 1123 compliant strategy title scoped to
// the environment's namespace and name, capped at 63 characters.
func UnleashStrategyTitle(namespace, envName string) string {
	var input string
	if namespace != "" {
		input = namespace + "/" + envName
	} else {
		input = envName
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(input)))[:8]

	base := fmt.Sprintf("diverge-%s", envName)
	if namespace != "" && namespace != "default" {
		base = fmt.Sprintf("diverge-%s-%s", namespace, envName)
	}

	maxBaseLen := 63 - 9 // leave room for '-' and 8-char hash
	if len(base) > maxBaseLen {
		base = base[:maxBaseLen]
	}
	base = strings.TrimRight(base, "-")
	return fmt.Sprintf("%s-%s", base, hash)
}

type unleashConnectionConfig struct {
	url         string
	projectID   string
	environment string
	adminToken  string
	clientToken string
}

// resolveConnection resolves Unleash endpoint and authentication tokens using 4-tier Secret/env resolution:
// Tier 1: Look for ConnectionRef Secret in env.Namespace.
// Tier 2: Look for ConnectionRef Secret in "diverge-system".
// Tier 3: Default secret names ("unleash-connection", "unleash-credentials") in env.Namespace or "diverge-system".
// Tier 4: Environment variables.
func (p *UnleashProvider) resolveConnection(ctx context.Context, env *v1alpha1.Environment) (*unleashConnectionConfig, error) {
	if env == nil {
		return nil, errors.New("environment cannot be nil")
	}

	secretName := ""
	if env.Spec.Features != nil && env.Spec.Features.ConnectionRef != "" {
		secretName = env.Spec.Features.ConnectionRef
	}

	var secret *corev1.Secret
	if secretName != "" && p.client != nil {
		sec := &corev1.Secret{}
		getErr := p.client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: env.Namespace}, sec)
		if getErr == nil {
			secret = sec
		} else if apierrors.IsNotFound(getErr) && env.Namespace != "diverge-system" {
			secSystem := &corev1.Secret{}
			if sysErr := p.client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: "diverge-system"}, secSystem); sysErr == nil {
				secret = secSystem
			} else if !apierrors.IsNotFound(sysErr) {
				return nil, fmt.Errorf("failed looking up unleash secret %q in diverge-system: %w", secretName, sysErr)
			}
		} else if !apierrors.IsNotFound(getErr) {
			return nil, fmt.Errorf("failed looking up unleash secret %q in %s: %w", secretName, env.Namespace, getErr)
		}

		if secret == nil {
			return nil, fmt.Errorf("unleash secret %q not found in namespace %q or %q", secretName, env.Namespace, "diverge-system")
		}
	} else if p.client != nil {
		for _, name := range []string{"unleash-connection", "unleash-credentials"} {
			sec := &corev1.Secret{}
			if err := p.client.Get(ctx, types.NamespacedName{Name: name, Namespace: env.Namespace}, sec); err == nil {
				secret = sec
				break
			}
			if env.Namespace != "diverge-system" {
				secSys := &corev1.Secret{}
				if err := p.client.Get(ctx, types.NamespacedName{Name: name, Namespace: "diverge-system"}, secSys); err == nil {
					secret = secSys
					break
				}
			}
		}
	}

	cfg := &unleashConnectionConfig{}
	urlFromSecret := false
	if secret != nil {
		cfg.url = extractSecretValue(secret, "url", "UNLEASH_URL", "endpoint", "UNLEASH_ENDPOINT", "UNLEASH_API_URL")
		urlFromSecret = cfg.url != ""
		cfg.adminToken = extractSecretValue(secret, "adminToken", "UNLEASH_ADMIN_TOKEN", "apiToken", "UNLEASH_API_TOKEN", "token")
		cfg.clientToken = extractSecretValue(secret, "clientToken", "UNLEASH_CLIENT_TOKEN", "sdkToken", "frontendToken")
		cfg.projectID = extractSecretValue(secret, "project", "projectId", "UNLEASH_PROJECT")
		cfg.environment = extractSecretValue(secret, "environment", "UNLEASH_ENVIRONMENT")
	}

	if cfg.url == "" {
		for _, envKey := range []string{"DIVERGE_UNLEASH_URL", "UNLEASH_URL", "UNLEASH_API_URL"} {
			if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
				cfg.url = v
				break
			}
		}
		if cfg.url == "" {
			cfg.url = "http://unleash.default.svc.cluster.local:4242"
		}
	}

	if cfg.adminToken == "" && !urlFromSecret {
		for _, envKey := range []string{"DIVERGE_UNLEASH_ADMIN_TOKEN", "UNLEASH_ADMIN_TOKEN", "UNLEASH_API_TOKEN"} {
			if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
				cfg.adminToken = v
				break
			}
		}
	}

	if cfg.clientToken == "" && !urlFromSecret {
		for _, envKey := range []string{"DIVERGE_UNLEASH_CLIENT_TOKEN", "UNLEASH_CLIENT_TOKEN"} {
			if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
				cfg.clientToken = v
				break
			}
		}
	}

	if cfg.projectID == "" {
		for _, envKey := range []string{"DIVERGE_UNLEASH_PROJECT", "UNLEASH_PROJECT"} {
			if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
				cfg.projectID = v
				break
			}
		}
		if cfg.projectID == "" {
			cfg.projectID = "default"
		}
	}

	if cfg.environment == "" {
		for _, envKey := range []string{"DIVERGE_UNLEASH_ENVIRONMENT", "UNLEASH_ENVIRONMENT"} {
			if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
				cfg.environment = v
				break
			}
		}
		if cfg.environment == "" {
			cfg.environment = "development"
		}
	}

	parsed, parseErr := url.Parse(strings.TrimSpace(cfg.url))
	if parseErr != nil {
		return nil, fmt.Errorf("invalid unleash url %q: %w", cfg.url, parseErr)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid unleash url scheme %q (must be http or https)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("invalid unleash url %q: host cannot be empty", cfg.url)
	}

	return cfg, nil
}

// UnleashManagedOverridesAnnotation records the comma-separated feature override keys
// managed by the last successful Unleash Provision reconcile so stale strategy cleanup
// only inspects removed overrides rather than scanning the entire Unleash project.
const UnleashManagedOverridesAnnotation = "divergedev.com/unleash-managed-overrides"

// Provision configures Unleash feature toggles and preview-scoped activation strategies for the environment.
func (p *UnleashProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*FeatureResult, error) {
	if env == nil {
		return nil, errors.New("environment cannot be nil")
	}

	conn, err := p.resolveConnection(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve unleash connection: %w", err)
	}

	unleashClient, err := NewUnleashClient(conn.url, conn.projectID, conn.environment, conn.adminToken, conn.clientToken, p.httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize unleash client: %w", err)
	}

	strategyTitle := UnleashStrategyTitle(env.Namespace, env.Name)

	var overrides map[string]string
	if env.Spec.Features != nil && env.Spec.Features.Overrides != nil {
		overrides = env.Spec.Features.Overrides
	}

	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := overrides[k]
		if _, err := unleashClient.EnsureFeature(ctx, k); err != nil {
			return nil, fmt.Errorf("failed to ensure unleash feature %s: %w", k, err)
		}

		var enabled bool
		var variantVal string
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true":
			enabled = true
		case "false":
			enabled = false
		default:
			enabled = true
			variantVal = v
		}

		if _, err := unleashClient.UpsertPreviewStrategy(ctx, k, strategyTitle, env.Name, enabled, variantVal); err != nil {
			return nil, fmt.Errorf("failed to configure unleash strategy for feature %s: %w", k, err)
		}

		if err := unleashClient.EnableFeatureInEnvironment(ctx, k); err != nil {
			return nil, fmt.Errorf("failed to enable unleash feature %s in environment %s: %w", k, conn.environment, err)
		}
	}

	// Clean up stale preview strategies only for previously managed override keys that were removed.
	var prevCSV string
	if env.Annotations != nil {
		prevCSV = env.Annotations[UnleashManagedOverridesAnnotation]
	}
	if prevCSV != "" {
		var staleFeatures []string
		for _, prevKey := range strings.Split(prevCSV, ",") {
			prevKey = strings.TrimSpace(prevKey)
			if prevKey == "" {
				continue
			}
			if _, active := overrides[prevKey]; !active {
				staleFeatures = append(staleFeatures, prevKey)
			}
		}
		if len(staleFeatures) > 0 {
			if err := unleashClient.DeletePreviewStrategies(ctx, strategyTitle, staleFeatures); err != nil {
				return nil, fmt.Errorf("failed to delete stale unleash strategies for %s: %w", strategyTitle, err)
			}
		}
	}

	// Persist current override keys in annotation for future stale cleanup diffs.
	currentCSV := strings.Join(keys, ",")
	if env.Annotations == nil {
		env.Annotations = make(map[string]string)
	}
	if currentCSV != "" {
		env.Annotations[UnleashManagedOverridesAnnotation] = currentCSV
	} else {
		delete(env.Annotations, UnleashManagedOverridesAnnotation)
	}
	if p.client != nil && env.Name != "" && env.Namespace != "" {
		var latest v1alpha1.Environment
		if getErr := p.client.Get(ctx, types.NamespacedName{Name: env.Name, Namespace: env.Namespace}, &latest); getErr == nil {
			if latest.Annotations == nil {
				latest.Annotations = make(map[string]string)
			}
			if currentCSV != "" {
				latest.Annotations[UnleashManagedOverridesAnnotation] = currentCSV
			} else {
				delete(latest.Annotations, UnleashManagedOverridesAnnotation)
			}
			_ = p.client.Update(ctx, &latest)
		}
	}

	p.logger.Info("Unleash preview strategies provisioned",
		"environment", env.Name,
		"strategyTitle", strategyTitle,
		"overrideCount", len(overrides))

	sdkToken := conn.clientToken
	sdkURL := strings.TrimRight(strings.TrimSpace(conn.url), "/")
	if !strings.HasSuffix(sdkURL, "/api") {
		sdkURL += "/api"
	}

	return &FeatureResult{
		ProviderType: "unleash",
		EnvVars: map[string]string{
			"UNLEASH_URL":                   sdkURL,
			"UNLEASH_API_TOKEN":             sdkToken,
			"UNLEASH_PROJECT":               conn.projectID,
			"UNLEASH_ENVIRONMENT":           conn.environment,
			"UNLEASH_STRATEGY_TITLE":        strategyTitle,
			"OPENFEATURE_UNLEASH_URL":       sdkURL,
			"OPENFEATURE_UNLEASH_API_TOKEN": sdkToken,
			"OPENFEATURE_TARGET_KEY":        strategyTitle,
		},
		Message: fmt.Sprintf("Unleash strategy %q provisioned in %s/%s with %d override(s)", strategyTitle, conn.projectID, conn.environment, len(overrides)),
	}, nil
}

// Teardown removes preview-scoped activation strategies from Unleash.
func (p *UnleashProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	if env == nil {
		return errors.New("environment cannot be nil")
	}

	conn, err := p.resolveConnection(ctx, env)
	if err != nil {
		p.logger.Error(err, "Failed to resolve unleash connection for teardown", "environment", env.Name)
		return fmt.Errorf("failed to resolve unleash connection for teardown: %w", err)
	}

	unleashClient, err := NewUnleashClient(conn.url, conn.projectID, conn.environment, conn.adminToken, conn.clientToken, p.httpClient)
	if err != nil {
		p.logger.Error(err, "Failed to initialize unleash client for teardown", "environment", env.Name)
		return fmt.Errorf("failed to initialize unleash client for teardown: %w", err)
	}

	strategyTitle := UnleashStrategyTitle(env.Namespace, env.Name)
	// Scan all project features so strategies of removed overrides are also deleted.
	if err := unleashClient.DeletePreviewStrategies(ctx, strategyTitle, nil); err != nil {
		p.logger.Error(err, "Failed to delete unleash preview strategies", "strategyTitle", strategyTitle)
		return fmt.Errorf("failed to delete unleash strategies for %s: %w", strategyTitle, err)
	}

	p.logger.Info("Unleash preview strategies deleted", "strategyTitle", strategyTitle)
	return nil
}

// Status returns readiness and connectivity of the Unleash provider.
func (p *UnleashProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*FeatureStatus, error) {
	if env == nil {
		return nil, errors.New("environment cannot be nil")
	}

	conn, err := p.resolveConnection(ctx, env)
	if err != nil {
		return &FeatureStatus{
			Ready:   false,
			Message: fmt.Sprintf("Unleash connection resolution failed: %v", err),
		}, nil
	}

	unleashClient, err := NewUnleashClient(conn.url, conn.projectID, conn.environment, conn.adminToken, conn.clientToken, p.httpClient)
	if err != nil {
		return &FeatureStatus{
			Ready:   false,
			Message: fmt.Sprintf("Unleash client initialization failed: %v", err),
		}, nil
	}

	if err := unleashClient.HealthCheck(ctx); err != nil {
		return &FeatureStatus{
			Ready:   false,
			Message: fmt.Sprintf("Unleash health check failed: %v", err),
		}, nil
	}

	return &FeatureStatus{
		Ready:   true,
		Message: fmt.Sprintf("Unleash provider active and healthy at %s", conn.url),
	}, nil
}
