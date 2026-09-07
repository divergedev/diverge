package features

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
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
	Providers.Register("flipt", registry.Provider[FeatureProvider]{
		Create: func(deps registry.Deps) (FeatureProvider, error) {
			return NewFliptProvider(deps.Client, deps.Scheme, deps.Logger), nil
		},
		Description: "Flipt feature flag provider (ephemeral namespace & remote configuration)",
	})
}

// FliptProvider implements FeatureProvider for Flipt feature flags management.
type FliptProvider struct {
	client     client.Client
	scheme     *runtime.Scheme
	logger     logr.Logger
	httpClient *http.Client
}

// NewFliptProvider creates a new FliptProvider.
func NewFliptProvider(c client.Client, scheme *runtime.Scheme, logger logr.Logger) *FliptProvider {
	return &FliptProvider{
		client: c,
		scheme: scheme,
		logger: logger,
	}
}

// WithHTTPClient allows overriding the default HTTP client (primarily for testing).
func (p *FliptProvider) WithHTTPClient(client *http.Client) *FliptProvider {
	p.httpClient = client
	return p
}

// Type returns "flipt".
func (p *FliptProvider) Type() string {
	return "flipt"
}

// FliptNamespaceName formats the ephemeral Flipt namespace name for a given environment.
func FliptNamespaceName(envName string) string {
	ns := fmt.Sprintf("diverge-%s", envName)
	if len(ns) > 63 {
		ns = ns[:63]
	}
	return ns
}

// resolveConnection resolves Flipt endpoint and authentication tokens using dual-tier Secret resolution.
// Tier 1: Look for ConnectionRef Secret in the environment's namespace (env.Namespace).
// Tier 2: Look for ConnectionRef Secret in the controller namespace ("diverge-system").
// Tier 3: Environment variables or default cluster DNS.
func (p *FliptProvider) resolveConnection(ctx context.Context, env *v1alpha1.Environment) (fliptURL, adminToken, clientToken string, err error) {
	if env == nil {
		return "", "", "", errors.New("environment cannot be nil")
	}

	secretName := ""
	if env.Spec.Features != nil && env.Spec.Features.ConnectionRef != "" {
		secretName = env.Spec.Features.ConnectionRef
	}

	var secret *corev1.Secret
	if secretName != "" && p.client != nil {
		// 1. Try env.Namespace
		sec := &corev1.Secret{}
		getErr := p.client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: env.Namespace}, sec)
		if getErr == nil {
			secret = sec
		} else if apierrors.IsNotFound(getErr) && env.Namespace != "diverge-system" {
			// 2. Try fallback namespace diverge-system
			secSystem := &corev1.Secret{}
			if sysErr := p.client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: "diverge-system"}, secSystem); sysErr == nil {
				secret = secSystem
			} else if !apierrors.IsNotFound(sysErr) {
				return "", "", "", fmt.Errorf("failed looking up flipt secret %q in diverge-system: %w", secretName, sysErr)
			}
		} else if !apierrors.IsNotFound(getErr) {
			return "", "", "", fmt.Errorf("failed looking up flipt secret %q in %s: %w", secretName, env.Namespace, getErr)
		}

		if secret == nil {
			return "", "", "", fmt.Errorf("flipt secret %q not found in namespace %q or %q", secretName, env.Namespace, "diverge-system")
		}
	} else if p.client != nil {
		// ConnectionRef was empty; check default secret names "flipt-connection" or "flipt-credentials"
		for _, name := range []string{"flipt-connection", "flipt-credentials"} {
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

	if secret != nil {
		fliptURL = extractSecretValue(secret, "url", "FLIPT_URL", "endpoint", "FLIPT_ENDPOINT")
		adminToken = extractSecretValue(secret, "adminToken", "FLIPT_ADMIN_TOKEN", "token", "FLIPT_TOKEN")
		clientToken = extractSecretValue(secret, "clientToken", "FLIPT_CLIENT_TOKEN")
		if clientToken == "" {
			clientToken = adminToken
		}
	}

	// Fallback to environment variables if URL is still empty
	if fliptURL == "" {
		if u := os.Getenv("DIVERGE_FLIPT_URL"); u != "" {
			fliptURL = u
		} else if u := os.Getenv("FLIPT_URL"); u != "" {
			fliptURL = u
		} else {
			fliptURL = "http://flipt.diverge-system.svc.cluster.local:8080"
		}
	}

	if adminToken == "" {
		if t := os.Getenv("DIVERGE_FLIPT_TOKEN"); t != "" {
			adminToken = t
			if clientToken == "" {
				clientToken = t
			}
		} else if t := os.Getenv("FLIPT_TOKEN"); t != "" {
			adminToken = t
			if clientToken == "" {
				clientToken = t
			}
		}
	}

	// SSRF validation
	parsed, parseErr := url.Parse(strings.TrimSpace(fliptURL))
	if parseErr != nil {
		return "", "", "", fmt.Errorf("invalid flipt url %q: %w", fliptURL, parseErr)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", "", fmt.Errorf("invalid flipt url scheme %q (must be http or https)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", "", "", fmt.Errorf("invalid flipt url %q: host cannot be empty", fliptURL)
	}

	return fliptURL, adminToken, clientToken, nil
}

func extractSecretValue(secret *corev1.Secret, keys ...string) string {
	for _, k := range keys {
		if val, exists := secret.Data[k]; exists && len(val) > 0 {
			return string(val)
		}
		if val, exists := secret.StringData[k]; exists && len(val) > 0 {
			return val
		}
	}
	return ""
}

// Provision sets up an isolated Flipt namespace for the environment and creates flag overrides.
func (p *FliptProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*FeatureResult, error) {
	if env == nil {
		return nil, errors.New("environment cannot be nil")
	}

	fliptURL, adminToken, clientToken, err := p.resolveConnection(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve flipt connection: %w", err)
	}

	client, err := NewFliptClient(fliptURL, adminToken, p.httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize flipt client: %w", err)
	}

	nsKey := FliptNamespaceName(env.Name)
	nsName := fmt.Sprintf("Diverge %s", env.Name)
	nsDesc := fmt.Sprintf("Ephemeral feature flags for Diverge preview environment %s", env.Name)

	if err := client.CreateNamespace(ctx, nsKey, nsName, nsDesc); err != nil {
		return nil, fmt.Errorf("failed to create flipt namespace %s: %w", nsKey, err)
	}

	var overrides map[string]string
	if env.Spec.Features != nil && env.Spec.Features.Overrides != nil {
		overrides = env.Spec.Features.Overrides
	}

	for k, v := range overrides {
		var flagType string
		var enabled bool
		var variantVal string

		switch v {
		case "true":
			flagType = "BOOLEAN_FLAG_TYPE"
			enabled = true
		case "false":
			flagType = "BOOLEAN_FLAG_TYPE"
			enabled = false
		default:
			flagType = "VARIANT_FLAG_TYPE"
			enabled = true
			variantVal = v
		}

		if err := client.CreateOrUpdateFlag(ctx, nsKey, k, flagType, enabled, variantVal); err != nil {
			return nil, fmt.Errorf("failed to configure flipt flag %s in namespace %s: %w", k, nsKey, err)
		}
	}

	envVars := map[string]string{
		"FLIPT_URL":       fliptURL,
		"FLIPT_NAMESPACE": nsKey,
	}
	if clientToken != "" {
		envVars["FLIPT_AUTH_TOKEN"] = clientToken
	}

	return &FeatureResult{
		ProviderType: "flipt",
		EnvVars:      envVars,
		Message:      fmt.Sprintf("Provisioned Flipt namespace %s with %d flag override(s)", nsKey, len(overrides)),
	}, nil
}

// Teardown deletes the ephemeral Flipt namespace and all contained flags atomically.
func (p *FliptProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	if env == nil {
		return nil
	}

	fliptURL, adminToken, _, err := p.resolveConnection(ctx, env)
	if err != nil {
		// If secret resolution fails during teardown (e.g. secret was deleted first),
		// do not block finalizer removal. Log and succeed gracefully.
		p.logger.Info("Skipping Flipt teardown because connection cannot be resolved", "error", err)
		return nil
	}

	client, err := NewFliptClient(fliptURL, adminToken, p.httpClient)
	if err != nil {
		return fmt.Errorf("failed to initialize flipt client for teardown: %w", err)
	}

	nsKey := FliptNamespaceName(env.Name)
	if err := client.DeleteNamespace(ctx, nsKey); err != nil && !errors.Is(err, ErrFliptNotFound) {
		return fmt.Errorf("failed to delete flipt namespace %s: %w", nsKey, err)
	}

	return nil
}

// Status checks whether the Flipt namespace is provisioned and reachable.
func (p *FliptProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*FeatureStatus, error) {
	if env == nil {
		return &FeatureStatus{Ready: false, Message: "Environment is nil"}, nil
	}

	// If no overrides are configured, report ready
	if env.Spec.Features == nil || len(env.Spec.Features.Overrides) == 0 {
		return &FeatureStatus{Ready: true, Message: "No feature overrides configured"}, nil
	}

	fliptURL, adminToken, _, err := p.resolveConnection(ctx, env)
	if err != nil {
		return &FeatureStatus{Ready: false, Message: fmt.Sprintf("Failed to resolve Flipt connection: %v", err)}, nil
	}

	client, err := NewFliptClient(fliptURL, adminToken, p.httpClient)
	if err != nil {
		return &FeatureStatus{Ready: false, Message: fmt.Sprintf("Failed to initialize Flipt client: %v", err)}, nil
	}

	nsKey := FliptNamespaceName(env.Name)
	if err := client.GetNamespace(ctx, nsKey); err != nil {
		if errors.Is(err, ErrFliptNotFound) {
			return &FeatureStatus{Ready: false, Message: fmt.Sprintf("Flipt namespace %s not found", nsKey)}, nil
		}
		return &FeatureStatus{Ready: false, Message: fmt.Sprintf("Failed to verify Flipt namespace %s: %v", nsKey, err)}, nil
	}

	return &FeatureStatus{
		Ready:   true,
		Message: fmt.Sprintf("Flipt namespace %s ready", nsKey),
	}, nil
}
