package features

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
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
	Providers.Register("flagsmith", registry.Provider[FeatureProvider]{
		Create: func(deps registry.Deps) (FeatureProvider, error) {
			return NewFlagsmithProvider(deps.Client, deps.Scheme, deps.Logger), nil
		},
		Description: "Flagsmith enterprise feature flag provider (identity & trait targeting, remote evaluation)",
	})
}

// FlagsmithProvider implements FeatureProvider for Flagsmith feature flag management.
type FlagsmithProvider struct {
	client     client.Client
	scheme     *runtime.Scheme
	logger     logr.Logger
	httpClient *http.Client
}

// NewFlagsmithProvider creates a new FlagsmithProvider.
func NewFlagsmithProvider(c client.Client, scheme *runtime.Scheme, logger logr.Logger) *FlagsmithProvider {
	return &FlagsmithProvider{
		client: c,
		scheme: scheme,
		logger: logger,
	}
}

// WithHTTPClient allows overriding the default HTTP client (primarily for testing).
func (p *FlagsmithProvider) WithHTTPClient(httpClient *http.Client) *FlagsmithProvider {
	p.httpClient = httpClient
	return p
}

// Type returns "flagsmith".
func (p *FlagsmithProvider) Type() string {
	return "flagsmith"
}

// FlagsmithIdentityName generates an RFC 1123 compliant identity name scoped to the environment's namespace and name,
// incorporating a stable hash so environments with identical or long name prefixes remain distinct (capped at 63 chars).
func FlagsmithIdentityName(namespace, envName string) string {
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

// resolveConnection resolves Flagsmith endpoint and authentication tokens using dual-tier Secret resolution.
// Tier 1: Look for ConnectionRef Secret in the environment's namespace (env.Namespace).
// Tier 2: Look for ConnectionRef Secret in the controller namespace ("diverge-system").
// Tier 3: Default secret names ("flagsmith-connection", "flagsmith-credentials") in env.Namespace or diverge-system.
// Tier 4: Environment variables.
func (p *FlagsmithProvider) resolveConnection(ctx context.Context, env *v1alpha1.Environment) (flagsmithURL, envKey, masterAPIKey string, err error) {
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
				return "", "", "", fmt.Errorf("failed looking up flagsmith secret %q in diverge-system: %w", secretName, sysErr)
			}
		} else if !apierrors.IsNotFound(getErr) {
			return "", "", "", fmt.Errorf("failed looking up flagsmith secret %q in %s: %w", secretName, env.Namespace, getErr)
		}

		if secret == nil {
			return "", "", "", fmt.Errorf("flagsmith secret %q not found in namespace %q or %q", secretName, env.Namespace, "diverge-system")
		}
	} else if p.client != nil {
		// ConnectionRef was empty; check default secret names "flagsmith-connection" or "flagsmith-credentials"
		for _, name := range []string{"flagsmith-connection", "flagsmith-credentials"} {
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
		flagsmithURL = extractSecretValue(secret, "url", "FLAGSMITH_URL", "endpoint", "FLAGSMITH_ENDPOINT", "FLAGSMITH_API_URL")
		envKey = extractSecretValue(secret, "environmentKey", "FLAGSMITH_ENVIRONMENT_KEY", "envKey", "clientKey", "apiKey")
		masterAPIKey = extractSecretValue(secret, "masterApiKey", "FLAGSMITH_MASTER_API_KEY", "adminToken", "masterKey", "token")
	}

	// Fallback to environment variables if values are still empty
	if flagsmithURL == "" {
		if u := os.Getenv("DIVERGE_FLAGSMITH_URL"); u != "" {
			flagsmithURL = u
		} else if u := os.Getenv("FLAGSMITH_URL"); u != "" {
			flagsmithURL = u
		} else if u := os.Getenv("FLAGSMITH_API_URL"); u != "" {
			flagsmithURL = u
		} else {
			flagsmithURL = "https://edge.api.flagsmith.com/api/v1"
		}
	}

	if envKey == "" {
		if k := os.Getenv("DIVERGE_FLAGSMITH_ENVIRONMENT_KEY"); k != "" {
			envKey = k
		} else if k := os.Getenv("FLAGSMITH_ENVIRONMENT_KEY"); k != "" {
			envKey = k
		}
	}

	if masterAPIKey == "" {
		if k := os.Getenv("DIVERGE_FLAGSMITH_MASTER_API_KEY"); k != "" {
			masterAPIKey = k
		} else if k := os.Getenv("FLAGSMITH_MASTER_API_KEY"); k != "" {
			masterAPIKey = k
		}
	}

	// SSRF validation
	parsed, parseErr := url.Parse(strings.TrimSpace(flagsmithURL))
	if parseErr != nil {
		return "", "", "", fmt.Errorf("invalid flagsmith url %q: %w", flagsmithURL, parseErr)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", "", fmt.Errorf("invalid flagsmith url scheme %q (must be http or https)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", "", "", fmt.Errorf("invalid flagsmith url %q: host cannot be empty", flagsmithURL)
	}

	return flagsmithURL, envKey, masterAPIKey, nil
}

// Provision sets up an isolated Flagsmith identity with traits and applies feature flag overrides.
func (p *FlagsmithProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*FeatureResult, error) {
	if env == nil {
		return nil, errors.New("environment cannot be nil")
	}

	flagsmithURL, envKey, masterAPIKey, err := p.resolveConnection(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve flagsmith connection: %w", err)
	}

	flagsmithClient, err := NewFlagsmithClient(flagsmithURL, envKey, masterAPIKey, p.httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize flagsmith client: %w", err)
	}

	identity := FlagsmithIdentityName(env.Namespace, env.Name)

	// Standard traits for Diverge preview environments
	traits := map[string]interface{}{
		"diverge_environment": env.Name,
		"diverge_namespace":   env.Namespace,
		"diverge_preview":     true,
	}

	if _, err := flagsmithClient.EnsureIdentityWithTraits(ctx, identity, traits); err != nil {
		return nil, fmt.Errorf("failed to ensure flagsmith identity %s: %w", identity, err)
	}

	var overrides map[string]string
	if env.Spec.Features != nil && env.Spec.Features.Overrides != nil {
		overrides = env.Spec.Features.Overrides
	}

	for k, v := range overrides {
		var enabled bool
		var val interface{}

		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true":
			enabled = true
			val = "true"
		case "false":
			enabled = false
			val = "false"
		default:
			// Attempt integer conversion
			if intVal, intErr := strconv.ParseInt(v, 10, 64); intErr == nil {
				enabled = true
				val = intVal
			} else if floatVal, floatErr := strconv.ParseFloat(v, 64); floatErr == nil {
				enabled = true
				val = floatVal
			} else {
				enabled = true
				val = v
			}
		}

		if _, err := flagsmithClient.SetIdentityFeatureState(ctx, identity, k, enabled, val); err != nil {
			return nil, fmt.Errorf("failed to set flagsmith flag %s for identity %s: %w", k, identity, err)
		}
	}

	p.logger.Info("Flagsmith identity and overrides provisioned",
		"environment", env.Name,
		"identity", identity,
		"overrideCount", len(overrides))

	return &FeatureResult{
		ProviderType: "flagsmith",
		EnvVars: map[string]string{
			"FLAGSMITH_ENVIRONMENT_KEY":             envKey,
			"FLAGSMITH_API_URL":                     flagsmithURL,
			"FLAGSMITH_IDENTITY":                    identity,
			"OPENFEATURE_FLAGSMITH_ENVIRONMENT_KEY": envKey,
			"OPENFEATURE_FLAGSMITH_API_URL":         flagsmithURL,
			"OPENFEATURE_TARGET_KEY":                identity,
		},
		Message: fmt.Sprintf("Flagsmith ephemeral identity %q provisioned with %d override(s)", identity, len(overrides)),
	}, nil
}

// Teardown cleans up the ephemeral Flagsmith identity.
func (p *FlagsmithProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	if env == nil {
		return errors.New("environment cannot be nil")
	}

	flagsmithURL, envKey, masterAPIKey, err := p.resolveConnection(ctx, env)
	if err != nil {
		p.logger.Error(err, "Failed to resolve flagsmith connection for teardown", "environment", env.Name)
		return fmt.Errorf("failed to resolve flagsmith connection for teardown: %w", err)
	}

	flagsmithClient, err := NewFlagsmithClient(flagsmithURL, envKey, masterAPIKey, p.httpClient)
	if err != nil {
		p.logger.Error(err, "Failed to initialize flagsmith client for teardown", "environment", env.Name)
		return fmt.Errorf("failed to initialize flagsmith client for teardown: %w", err)
	}

	identity := FlagsmithIdentityName(env.Namespace, env.Name)
	if err := flagsmithClient.DeleteIdentity(ctx, identity); err != nil && !errors.Is(err, ErrFlagsmithNotFound) {
		p.logger.Error(err, "Failed to delete ephemeral flagsmith identity", "identity", identity)
		return fmt.Errorf("failed to delete flagsmith identity %s: %w", identity, err)
	}

	p.logger.Info("Flagsmith ephemeral identity deleted", "identity", identity)
	return nil
}

// Status returns readiness and connectivity of the Flagsmith provider.
func (p *FlagsmithProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*FeatureStatus, error) {
	if env == nil {
		return nil, errors.New("environment cannot be nil")
	}

	flagsmithURL, envKey, masterAPIKey, err := p.resolveConnection(ctx, env)
	if err != nil {
		return &FeatureStatus{
			Ready:   false,
			Message: fmt.Sprintf("Flagsmith connection resolution failed: %v", err),
		}, nil
	}

	flagsmithClient, err := NewFlagsmithClient(flagsmithURL, envKey, masterAPIKey, p.httpClient)
	if err != nil {
		return &FeatureStatus{
			Ready:   false,
			Message: fmt.Sprintf("Flagsmith client initialization failed: %v", err),
		}, nil
	}

	if err := flagsmithClient.HealthCheck(ctx); err != nil {
		return &FeatureStatus{
			Ready:   false,
			Message: fmt.Sprintf("Flagsmith health check failed: %v", err),
		}, nil
	}

	return &FeatureStatus{
		Ready:   true,
		Message: fmt.Sprintf("Flagsmith provider active and healthy at %s", flagsmithURL),
	}, nil
}
