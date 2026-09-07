package secrets

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrUnknownSecretProvider is returned when a secret reference uses an unregistered provider.
var ErrUnknownSecretProvider = errors.New("unknown secret provider")

// Resolver resolves secret references to their plaintext values.
type Resolver interface {
	Resolve(ctx context.Context, ref SecretRef) (string, error)
}

// SecretRef identifies a secret value.
type SecretRef struct {
	// Provider is the secret backend: "env", "file", "vault", "openbao", "bao"
	Provider string
	// Path is provider-specific: env var name, file path, or Vault/OpenBao path
	Path string
	// Key is the key within the secret (for Vault/OpenBao KV v2)
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
		// Fallback for openbao / bao aliases to vault
		lower := strings.ToLower(ref.Provider)
		if lower == "openbao" || lower == "bao" {
			r, ok = m.resolvers["vault"]
		}
	}
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownSecretProvider, ref.Provider)
	}
	return r.Resolve(ctx, ref)
}
