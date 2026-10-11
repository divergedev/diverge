package auth

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgauth "github.com/divergedev/diverge/pkg/auth"
)

func TestMacaroonAuthProvider(t *testing.T) {
	ctx := context.Background()
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	macProvider := pkgauth.NewMacaroonProvider(rootKey)
	store := pkgauth.NewInMemoryTokenStore()

	authProvider := NewMacaroonAuthProvider(macProvider, store)

	tok, err := macProvider.Mint(ctx, pkgauth.Claims{
		TaskID:       "fix-bug",
		Namespace:    "staging",
		RepoURL:      "https://github.com/org/repo",
		Branch:       "main",
		AllowedTools: []string{"diverge_*"},
		ExpiresAt:    time.Now().Add(30 * time.Minute),
	})
	require.NoError(t, err)

	raw, err := tok.Serialize()
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(ctx, "fix-bug", "staging", raw))

	// 1. Authenticate with raw JSON token
	user, err := authProvider.Authenticate(ctx, string(raw))
	require.NoError(t, err)
	assert.Equal(t, "system:agenttask:staging:fix-bug", user.Username)
	assert.Equal(t, []string{"staging"}, []string(user.Extra[ExtraKeyNamespace]))
	assert.Equal(t, []string{"macaroon"}, []string(user.Extra[ExtraKeyAuthType]))

	// 2. Authenticate with base64-encoded token
	b64Token := base64.RawStdEncoding.EncodeToString(raw)
	userB64, err := authProvider.Authenticate(ctx, b64Token)
	require.NoError(t, err)
	assert.Equal(t, user.Username, userB64.Username)

	// 2b. Attenuated token derived from the current stored root token succeeds
	attenuatedTok, err := macProvider.Attenuate(ctx, tok, pkgauth.Caveat{
		Key:   "allowed_tools",
		Op:    pkgauth.OpGlob,
		Value: "diverge_get_*",
	})
	require.NoError(t, err)
	attenuatedRaw, err := attenuatedTok.Serialize()
	require.NoError(t, err)
	attenuatedUser, err := authProvider.Authenticate(ctx, string(attenuatedRaw))
	require.NoError(t, err)
	assert.Equal(t, []string{"diverge_get_*"}, []string(attenuatedUser.Extra[ExtraKeyAllowedTools]))

	// 3. Revoking token in TokenStore causes Authenticate to fail with ErrTokenRevoked
	require.NoError(t, store.RevokeToken(ctx, "fix-bug", "staging"))
	_, err = authProvider.Authenticate(ctx, b64Token)
	assert.ErrorIs(t, err, pkgauth.ErrTokenRevoked)

	// 3b. Re-minting and saving a replacement token (e.g. after pause -> resume) invalidates the previous token
	replacementTok, err := macProvider.Mint(ctx, pkgauth.Claims{
		TaskID:       "fix-bug",
		Namespace:    "staging",
		RepoURL:      "https://github.com/org/repo",
		Branch:       "main",
		AllowedTools: []string{"diverge_*"},
		ExpiresAt:    time.Now().Add(30 * time.Minute),
	})
	require.NoError(t, err)
	replacementRaw, err := replacementTok.Serialize()
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(ctx, "fix-bug", "staging", replacementRaw))

	// Old root token and old attenuated token are rejected because their nonce no longer matches the stored token
	_, err = authProvider.Authenticate(ctx, b64Token)
	assert.ErrorIs(t, err, pkgauth.ErrTokenRevoked)
	_, err = authProvider.Authenticate(ctx, string(attenuatedRaw))
	assert.ErrorIs(t, err, pkgauth.ErrTokenRevoked)

	// Replacement token succeeds
	_, err = authProvider.Authenticate(ctx, string(replacementRaw))
	require.NoError(t, err)

	// 4. Malformed token is rejected
	_, err = authProvider.Authenticate(ctx, "not-a-macaroon")
	assert.Error(t, err)

	// 5. WithTaskActiveCheck verifies AgentTask CRD lifecycle state
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fix-bug",
			Namespace: "staging",
		},
		Status: v1alpha1.AgentTaskStatus{
			Phase: v1alpha1.AgentTaskPhaseActive,
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(task).Build()
	crdProvider := NewMacaroonAuthProvider(macProvider, nil).WithTaskActiveCheck(k8sClient)

	_, err = crdProvider.Authenticate(ctx, b64Token)
	require.NoError(t, err)

	// Suspend task -> rejected with ErrTokenRevoked
	task.Spec.Suspended = true
	require.NoError(t, k8sClient.Update(ctx, task))
	_, err = crdProvider.Authenticate(ctx, b64Token)
	assert.ErrorIs(t, err, pkgauth.ErrTokenRevoked)
}
