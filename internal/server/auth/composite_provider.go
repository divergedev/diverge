package auth

import (
	"context"
	"fmt"
	"log/slog"
)

// CompositeProvider tries multiple AuthProviders in order, returning on the
// first successful authentication. This allows coexistence of OIDC sessions,
// GitHub sessions, and Kubernetes ServiceAccount tokens.
type CompositeProvider struct {
	providers []namedProvider
	logger    *slog.Logger
}

type namedProvider struct {
	name     string
	provider AuthProvider
}

// NewCompositeProvider creates a composite provider that tries providers in order.
// The first provider to successfully authenticate wins. All providers must fail
// for the authentication to be rejected.
func NewCompositeProvider(logger *slog.Logger) *CompositeProvider {
	return &CompositeProvider{
		logger: logger,
	}
}

// Add registers a named auth provider. Providers are tried in registration order.
func (cp *CompositeProvider) Add(name string, provider AuthProvider) {
	cp.providers = append(cp.providers, namedProvider{name: name, provider: provider})
}

// Authenticate tries each registered provider in order. Returns the first
// successful result. If all providers fail, returns a combined error.
func (cp *CompositeProvider) Authenticate(ctx context.Context, token string) (*UserInfo, error) {
	var lastErr error

	for _, np := range cp.providers {
		user, err := np.provider.Authenticate(ctx, token)
		if err == nil {
			cp.logger.Debug("authentication succeeded", "provider", np.name, "user", user.Username)
			return user, nil
		}
		lastErr = err
		cp.logger.Debug("authentication failed, trying next provider", "provider", np.name, "error", err)
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all auth providers failed: %w", lastErr)
	}
	return nil, fmt.Errorf("no auth providers configured")
}
