package sdk

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func clearStorageEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DIVERGE_STORAGE_PREFIX", "")
	t.Setenv("S3_KEY_PREFIX", "")
	t.Setenv("AWS_S3_PREFIX", "")
	t.Setenv("GCS_KEY_PREFIX", "")
	t.Setenv("DIVERGE_ENV", "")
}

func TestResolveStoragePrefix_And_Key(t *testing.T) {
	t.Run("baseline no-op when no env or prefix set", func(t *testing.T) {
		clearStorageEnv(t)

		ctx := context.Background()
		assert.Equal(t, "", ResolveStoragePrefix(ctx))
		assert.Equal(t, "avatars/user-123.png", ResolveStorageKey(ctx, "avatars/user-123.png"))
		assert.Equal(t, "/avatars/user-123.png", ResolveStorageKey(ctx, "/avatars/user-123.png"))
	})

	t.Run("context environment derives previews/<env>/ prefix and is idempotent", func(t *testing.T) {
		clearStorageEnv(t)

		ctx := WithEnvironment(context.Background(), "pr-42")
		assert.Equal(t, "previews/pr-42/", ResolveStoragePrefix(ctx))

		key := ResolveStorageKey(ctx, "avatars/user-123.png")
		assert.Equal(t, "previews/pr-42/avatars/user-123.png", key)
		assert.Equal(t, "previews/pr-42/avatars/user-123.png", ResolveStorageKey(ctx, key))
		assert.Equal(t, "previews/pr-42/avatars/user-123.png", ResolveStorageKey(ctx, "/avatars/user-123.png"))
		assert.Equal(t, "previews/pr-42/avatars/user-123.png", ResolveStorageKey(ctx, "/previews/pr-42/avatars/user-123.png"))
	})

	t.Run("explicit prefix env vars precedence and slash normalization", func(t *testing.T) {
		clearStorageEnv(t)
		t.Setenv("DIVERGE_STORAGE_PREFIX", "/previews/pr-42/uploads")
		t.Setenv("S3_KEY_PREFIX", "previews/other/")

		ctx := context.Background()
		assert.Equal(t, "previews/pr-42/uploads/", ResolveStoragePrefix(ctx))
		assert.Equal(t, "previews/pr-42/uploads/doc.pdf", ResolveStorageKey(ctx, "doc.pdf"))
		assert.Equal(t, "previews/pr-42/uploads/doc.pdf", ResolveStorageKey(ctx, "/doc.pdf"))
		assert.Equal(t, "previews/pr-42/uploads/doc.pdf", ResolveStorageKey(ctx, "previews/pr-42/uploads/doc.pdf"))
	})

	t.Run("S3_KEY_PREFIX, AWS_S3_PREFIX, and GCS_KEY_PREFIX fallbacks", func(t *testing.T) {
		clearStorageEnv(t)
		t.Setenv("S3_KEY_PREFIX", "previews/pr-55/s3")
		assert.Equal(t, "previews/pr-55/s3/", ResolveStoragePrefix(nil)) //nolint:staticcheck

		clearStorageEnv(t)
		t.Setenv("AWS_S3_PREFIX", "previews/pr-66/aws/")
		assert.Equal(t, "previews/pr-66/aws/", ResolveStoragePrefix(nil)) //nolint:staticcheck

		clearStorageEnv(t)
		t.Setenv("GCS_KEY_PREFIX", "/previews/pr-77/gcs/")
		assert.Equal(t, "previews/pr-77/gcs/", ResolveStoragePrefix(nil))                  //nolint:staticcheck
		assert.Equal(t, "previews/pr-77/gcs/blob.bin", ResolveStorageKey(nil, "blob.bin")) //nolint:staticcheck
	})

	t.Run("DIVERGE_ENV fallback when context has no environment", func(t *testing.T) {
		clearStorageEnv(t)
		t.Setenv("DIVERGE_ENV", "pr-88")

		assert.Equal(t, "previews/pr-88/", ResolveStoragePrefix(context.Background()))
		assert.Equal(t, "previews/pr-88/report.csv", ResolveStorageKey(context.Background(), "report.csv"))
	})
}

func TestPropertyResolveStoragePrefix(t *testing.T) {
	clearStorageEnv(t)

	rapid.Check(t, func(rt *rapid.T) {
		envName := rapid.StringMatching(`^[a-z0-9][a-z0-9-]{0,30}$`).Draw(rt, "envName")
		ctx := WithEnvironment(context.Background(), envName)

		prefix := ResolveStoragePrefix(ctx)
		require.Equal(rt, "previews/"+envName+"/", prefix)
		require.False(rt, strings.HasPrefix(prefix, "/"))
		require.True(rt, strings.HasSuffix(prefix, "/"))
	})
}

func TestPropertyResolveStorageKeyIdempotent(t *testing.T) {
	clearStorageEnv(t)

	rapid.Check(t, func(rt *rapid.T) {
		envName := rapid.StringMatching(`^[a-z0-9][a-z0-9-]{0,24}$`).Draw(rt, "envName")
		rawKey := rapid.String().Draw(rt, "rawKey")
		ctx := WithEnvironment(context.Background(), envName)

		first := ResolveStorageKey(ctx, rawKey)
		second := ResolveStorageKey(ctx, first)
		require.Equal(rt, first, second, "ResolveStorageKey must be idempotent")
		require.True(rt, strings.HasPrefix(first, "previews/"+envName+"/"))
	})
}
