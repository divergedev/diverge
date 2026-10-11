package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgauth "github.com/divergedev/diverge/pkg/auth"
)

const (
	// ExtraKeyAuthType identifies the authentication mechanism in UserInfo.Extra.
	ExtraKeyAuthType = "divergedev.com/auth-type"
	// ExtraKeyTaskID stores the authenticated AgentTask name in UserInfo.Extra.
	ExtraKeyTaskID = "divergedev.com/task-id"
	// ExtraKeyNamespace stores the authorized namespace in UserInfo.Extra.
	ExtraKeyNamespace = "divergedev.com/namespace"
	// ExtraKeyAllowedTools stores the glob patterns of allowed tools in UserInfo.Extra.
	ExtraKeyAllowedTools = "divergedev.com/allowed-tools"
)

// MacaroonAuthProvider authenticates AgentTask Macaroon capability tokens and checks revocation state.
type MacaroonAuthProvider struct {
	provider *pkgauth.MacaroonProvider
	store    pkgauth.TokenStore
	k8s      client.Client
}

var _ AuthProvider = (*MacaroonAuthProvider)(nil)

// NewMacaroonAuthProvider creates an AuthProvider backed by a MacaroonProvider and optional TokenStore.
func NewMacaroonAuthProvider(provider *pkgauth.MacaroonProvider, store pkgauth.TokenStore) *MacaroonAuthProvider {
	return &MacaroonAuthProvider{
		provider: provider,
		store:    store,
	}
}

// WithTaskActiveCheck configures an optional controller-runtime client to verify that
// the associated AgentTask still exists and is not suspended, paused, or in a terminal phase.
func (p *MacaroonAuthProvider) WithTaskActiveCheck(c client.Client) *MacaroonAuthProvider {
	p.k8s = c
	return p
}

// Authenticate verifies a raw or base64-encoded Macaroon capability token.
func (p *MacaroonAuthProvider) Authenticate(ctx context.Context, token string) (*UserInfo, error) {
	if p.provider == nil {
		return nil, fmt.Errorf("macaroon provider not configured")
	}

	raw, err := decodeMacaroonToken(token)
	if err != nil {
		return nil, err
	}

	_, claims, err := p.provider.VerifyAndExtract(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("macaroon verification failed: %w", err)
	}

	ns := claims.Namespace
	if ns == "" {
		ns = "default"
	}

	if p.store != nil && p.store.IsRevoked(ctx, claims.TaskID, ns) {
		return nil, pkgauth.ErrTokenRevoked
	}
	if p.k8s != nil {
		var task v1alpha1.AgentTask
		if err := p.k8s.Get(ctx, client.ObjectKey{Namespace: ns, Name: claims.TaskID}, &task); err != nil {
			return nil, pkgauth.ErrTokenRevoked
		}
		if task.DeletionTimestamp != nil || task.Spec.Suspended {
			return nil, pkgauth.ErrTokenRevoked
		}
		switch task.Status.Phase {
		case v1alpha1.AgentTaskPhasePausing, v1alpha1.AgentTaskPhasePaused, v1alpha1.AgentTaskPhaseCompleted, v1alpha1.AgentTaskPhaseFailed:
			return nil, pkgauth.ErrTokenRevoked
		}
	}

	allowedTools := claims.AllowedTools
	if len(allowedTools) == 0 {
		allowedTools = []string{"diverge_*"}
	}

	return &UserInfo{
		Username: fmt.Sprintf("system:agenttask:%s:%s", ns, claims.TaskID),
		UID:      claims.TaskID,
		Groups:   []string{"system:authenticated", "divergedev:agenttasks"},
		Extra: map[string]authorizationv1.ExtraValue{
			ExtraKeyAuthType:     {"macaroon"},
			ExtraKeyTaskID:       {claims.TaskID},
			ExtraKeyNamespace:    {ns},
			ExtraKeyAllowedTools: allowedTools,
		},
	}, nil
}

func decodeMacaroonToken(token string) ([]byte, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return nil, pkgauth.ErrMalformedToken
	}
	if strings.HasPrefix(trimmed, "{") {
		return []byte(trimmed), nil
	}
	for _, enc := range []*base64.Encoding{
		base64.RawStdEncoding,
		base64.StdEncoding,
		base64.RawURLEncoding,
		base64.URLEncoding,
	} {
		if decoded, err := enc.DecodeString(trimmed); err == nil && len(decoded) > 0 && decoded[0] == '{' {
			return decoded, nil
		}
	}
	return nil, pkgauth.ErrMalformedToken
}
