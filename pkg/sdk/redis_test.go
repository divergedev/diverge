package sdk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveRedisPrefix_And_Key(t *testing.T) {
	t.Run("baseline no-op when no env or prefix set", func(t *testing.T) {
		t.Setenv("DIVERGE_REDIS_PREFIX", "")
		t.Setenv("REDIS_KEY_PREFIX", "")
		t.Setenv("DIVERGE_ENV", "")

		ctx := context.Background()
		assert.Equal(t, "", ResolveRedisPrefix(ctx))
		assert.Equal(t, "user:123:profile", ResolveRedisKey(ctx, "user:123:profile"))
	})

	t.Run("context environment derives preview:<env>: prefix and is idempotent", func(t *testing.T) {
		t.Setenv("DIVERGE_REDIS_PREFIX", "")
		t.Setenv("REDIS_KEY_PREFIX", "")
		t.Setenv("DIVERGE_ENV", "")

		ctx := WithEnvironment(context.Background(), "pr-42")
		assert.Equal(t, "preview:pr-42:", ResolveRedisPrefix(ctx))

		key := ResolveRedisKey(ctx, "user:123:profile")
		assert.Equal(t, "preview:pr-42:user:123:profile", key)
		// Idempotent when called again with an already-prefixed key
		assert.Equal(t, "preview:pr-42:user:123:profile", ResolveRedisKey(ctx, key))
	})

	t.Run("DIVERGE_REDIS_PREFIX overrides and normalizes trailing colon", func(t *testing.T) {
		t.Setenv("DIVERGE_REDIS_PREFIX", "preview:pr-42:cache")
		t.Setenv("REDIS_KEY_PREFIX", "")
		t.Setenv("DIVERGE_ENV", "pr-42")

		ctx := context.Background()
		assert.Equal(t, "preview:pr-42:cache:", ResolveRedisPrefix(ctx))
		assert.Equal(t, "preview:pr-42:cache:session:abc", ResolveRedisKey(ctx, "session:abc"))
		assert.Equal(t, "preview:pr-42:cache:session:abc", ResolveRedisKey(ctx, "preview:pr-42:cache:session:abc"))
	})

	t.Run("REDIS_KEY_PREFIX fallback and nil context support", func(t *testing.T) {
		t.Setenv("DIVERGE_REDIS_PREFIX", "")
		t.Setenv("REDIS_KEY_PREFIX", "preview:pr-77:")
		t.Setenv("DIVERGE_ENV", "")

		assert.Equal(t, "preview:pr-77:", ResolveRedisPrefix(nil))                      //nolint:staticcheck
		assert.Equal(t, "preview:pr-77:rate-limit", ResolveRedisKey(nil, "rate-limit")) //nolint:staticcheck
	})
}
