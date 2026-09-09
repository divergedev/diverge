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
	Providers.Register("unleash", registry.Provider[FeatureProvider]{
		Create: func(deps registry.Deps) (FeatureProvider, error) {
			return NewUnleashProvider(deps.Client, deps.Scheme, deps.Logger), nil
		},
		Description: "Unleash feature flag provider (manages environments and strategy constraints)",
	})
}

// UnleashProvider is a feature flag provider for Unleash.
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

// WithHTTPClient allows injecting a custom HTTP client for testing.
func (p *UnleashProvider) WithHTTPClient(httpClient *http.Client) *UnleashProvider {
	p.httpClient = httpClient
	return p
}

// Type returns "unleash".
func (p *UnleashProvider) Type() string {
	return "unleash"
}

// UnleashEnvironmentName safely generates an Unleash-compatible environment name.
func UnleashEnvironmentName(envName string) string {
	name := fmt.Sprintf("diverge-%s", envName)
	if len(name) > 63 {
		return name[:63]
	}
	return name
}

func (p *UnleashProvider) resolveConnection(ctx context.Context, env *v1alpha1.Environment) (unleashURL, adminToken string, err error) {
	if env == nil {
		return "", "", errors.New("environment cannot be nil")
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
				return "", "", fmt.Errorf("failed looking up unleash secret %q in diverge-system: %w", secretName, sysErr)
			}
		} else if !apierrors.IsNotFound(getErr) {
			return "", "", fmt.Errorf("failed looking up unleash secret %q in %s: %w", secretName, env.Namespace, getErr)
		}

		if secret == nil {
			return "", "", fmt.Errorf("unleash secret %q not found in namespace %q or %q", secretName, env.Namespace, "diverge-system")
		}
	} else if p.client != nil {
		// ConnectionRef was empty; check default secret names "unleash-connection" or "unleash-credentials"
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

	if secret != nil {
		unleashURL = extractSecretValue(secret, "url", "UNLEASH_URL", "endpoint", "UNLEASH_ENDPOINT")
		adminToken = extractSecretValue(secret, "adminToken", "UNLEASH_ADMIN_TOKEN", "token", "UNLEASH_TOKEN", "UNLEASH_API_TOKEN")
	}

	// Fallback to environment variables if URL is still empty
	if unleashURL == "" {
		if u := os.Getenv("DIVERGE_UNLEASH_URL"); u != "" {
			unleashURL = u
		} else if u := os.Getenv("UNLEASH_URL"); u != "" {
			unleashURL = u
		} else {
			unleashURL = "http://unleash.diverge-system.svc.cluster.local:4242"
		}
	}

	if adminToken == "" {
		if t := os.Getenv("DIVERGE_UNLEASH_TOKEN"); t != "" {
			adminToken = t
		} else if t := os.Getenv("UNLEASH_API_TOKEN"); t != "" {
			adminToken = t
		}
	}

	// SSRF validation
	parsed, err := url.Parse(unleashURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid unleash URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("invalid unleash url scheme %q (must be http or https)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", "", errors.New("invalid unleash url host cannot be empty")
	}

	return unleashURL, adminToken, nil
}

// Provision sets up preview configuration for Unleash.
func (p *UnleashProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*FeatureResult, error) {
	unleashURL, adminToken, err := p.resolveConnection(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve unleash connection: %w", err)
	}

	unleashEnvName := UnleashEnvironmentName(env.Name)

	if len(env.Spec.Features.Overrides) > 0 {
		client, err := NewUnleashClient(unleashURL, adminToken, p.httpClient)
		if err != nil {
			return nil, fmt.Errorf("failed to create unleash client: %w", err)
		}

		for key, value := range env.Spec.Features.Overrides {
			enabled := true
			if strings.ToLower(value) == "false" {
				enabled = false
			}

			// Add constraint
			err = client.AddStrategyConstraint(ctx, "default", key, unleashEnvName, nil)
			if err != nil {
				p.logger.Error(err, "failed to add strategy constraint", "feature", key)
			}

			// Enable/disable
			err = client.EnableFeature(ctx, "default", key, unleashEnvName, enabled)
			if err != nil {
				return nil, fmt.Errorf("failed to set unleash feature %s: %w", key, err)
			}
		}
	}

	envVars := map[string]string{
		"UNLEASH_URL":         unleashURL,
		"UNLEASH_APP_NAME":    unleashEnvName,
		"UNLEASH_ENVIRONMENT": env.Name,
		"UNLEASH_INSTANCE_ID": unleashEnvName,
	}

	if adminToken != "" {
		envVars["UNLEASH_API_TOKEN"] = adminToken
	}

	p.logger.Info("Unleash feature provider provisioned", "environment", env.Name, "appName", unleashEnvName)

	return &FeatureResult{
		ProviderType: "unleash",
		EnvVars:      envVars,
		Message:      "Unleash environment configured successfully",
	}, nil
}

// Teardown cleans up Unleash ephemeral resources.
func (p *UnleashProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	_, _, err := p.resolveConnection(ctx, env)
	if err != nil {
		p.logger.Error(err, "failed to resolve unleash connection during teardown, skipping cleanup")
		return nil
	}

	// No cleanup needed for strategy-constraint approach (toggles revert when env is deleted)
	p.logger.Info("Unleash feature provider teardown complete", "environment", env.Name)
	return nil
}

// Status returns readiness of the Unleash provider.
func (p *UnleashProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*FeatureStatus, error) {
	if env == nil || env.Spec.Features == nil || len(env.Spec.Features.Overrides) == 0 {
		return &FeatureStatus{
			Ready:   true,
			Message: "No Unleash overrides specified",
		}, nil
	}

	unleashURL, adminToken, err := p.resolveConnection(ctx, env)
	if err != nil {
		return &FeatureStatus{
			Ready:   false,
			Message: fmt.Sprintf("Configuration error: %v", err),
		}, nil
	}

	client, err := NewUnleashClient(unleashURL, adminToken, p.httpClient)
	if err != nil {
		return &FeatureStatus{
			Ready:   false,
			Message: fmt.Sprintf("Client error: %v", err),
		}, nil
	}

	if err := client.HealthCheck(ctx); err != nil {
		return &FeatureStatus{
			Ready:   false,
			Message: fmt.Sprintf("Unleash health check failed: %v", err),
		}, nil
	}

	return &FeatureStatus{
		Ready:   true,
		Message: "Unleash connected and healthy",
	}, nil
}
