package secrets

import (
	"context"
	"errors"
	"fmt"
)

// ErrUnknownSecretProvider is returned when a secret reference uses an unregistered provider.
var ErrUnknownSecretProvider = errors.New("unknown secret provider")

// Resolver resolves secret references to their plaintext values.
type Resolver interface {
	Resolve(ctx context.Context, ref SecretRef) (string, error)
}

// SecretRef identifies a secret value.
type SecretRef struct {
	// Provider is the secret backend: "env", "file", "vault"
	Provider string
	// Path is provider-specific: env var name, file path, or Vault path
	Path string
	// Key is the key within the secret (for Vault KV v2)
	Key string
}

// Multi chains multiple resolvers by provider name.
type Multi struct {
	resolvers map[string]Resolver
}

func NewMulti(resolvers map[string]Resolver) *Multi {
	return &Multi{resolvers: resolvers}
}

func (m *Multi) Resolve(ctx context.Context, ref SecretRef) (string, error) {
	r, ok := m.resolvers[ref.Provider]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownSecretProvider, ref.Provider)
	}
	return r.Resolve(ctx, ref)
}
