package auth

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestCacheSetGet(t *testing.T) {
	cache := NewTokenCache(10, time.Minute)
	user := &UserInfo{Username: "test"}
	cache.Set("token1", user)

	got := cache.Get("token1")
	require.NotNil(t, got)
	assert.Equal(t, user, got)

	assert.Nil(t, cache.Get("nonexistent"))
}

func TestCacheExpiration(t *testing.T) {
	cache := NewTokenCache(10, time.Millisecond*50)
	user := &UserInfo{Username: "test"}
	cache.Set("token1", user)

	got := cache.Get("token1")
	require.NotNil(t, got)

	time.Sleep(time.Millisecond * 60)
	assert.Nil(t, cache.Get("token1"))
}

func TestCacheEviction(t *testing.T) {
	cache := NewTokenCache(2, time.Minute)
	cache.Set("token1", &UserInfo{Username: "user1"})
	cache.Set("token2", &UserInfo{Username: "user2"})
	cache.Set("token3", &UserInfo{Username: "user3"})

	assert.Nil(t, cache.Get("token1"))
	assert.NotNil(t, cache.Get("token2"))
	assert.NotNil(t, cache.Get("token3"))
}

func TestCacheUpdateExistingKey(t *testing.T) {
	cache := NewTokenCache(2, time.Minute)
	cache.Set("token1", &UserInfo{Username: "user1"})
	cache.Set("token2", &UserInfo{Username: "user2"})

	// update token1, shouldn't evict token2
	cache.Set("token1", &UserInfo{Username: "user1-updated"})

	assert.NotNil(t, cache.Get("token1"))
	assert.NotNil(t, cache.Get("token2"))
	assert.Equal(t, "user1-updated", cache.Get("token1").Username)
}

func TestCacheGetPromotesEntry(t *testing.T) {
	// Get promotes the entry in the LRU cache.
	cache := NewTokenCache(2, time.Minute)
	cache.Set("token1", &UserInfo{Username: "user1"})
	cache.Set("token2", &UserInfo{Username: "user2"})

	// Access token1 — promotes it to newest
	_ = cache.Get("token1")

	// Insert token3 — should evict token2 (oldest, since token1 was promoted)
	cache.Set("token3", &UserInfo{Username: "user3"})

	assert.NotNil(t, cache.Get("token1"), "token1 should survive (promoted by Get)")
	assert.Nil(t, cache.Get("token2"), "token2 should be evicted (oldest after promotion)")
	assert.NotNil(t, cache.Get("token3"), "token3 should exist (just inserted)")
}

func TestCacheConcurrentAccess(t *testing.T) {
	cache := NewTokenCache(100, time.Minute)
	var wg sync.WaitGroup

	require.NotPanics(t, func() {
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				token := fmt.Sprintf("token-%d", idx)
				cache.Set(token, &UserInfo{Username: fmt.Sprintf("user-%d", idx)})
				cache.Get(token)
			}(i)
		}
		wg.Wait()
	})
}

func TestCacheHashToken(t *testing.T) {
	h1 := hashToken("my-secret-token")
	h2 := hashToken("my-secret-token")
	assert.Equal(t, h1, h2)
	assert.NotEqual(t, h1, hashToken("another-token"))
	assert.NotContains(t, h1, "my-secret-token")
}

func TestCache_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		maxSize := rapid.IntRange(1, 50).Draw(t, "maxSize")
		cache := NewTokenCache(maxSize, time.Millisecond*50)

		tokens := rapid.SliceOfN(rapid.String(), 1, 100).Draw(t, "tokens")

		// Property: Get after Set always returns the same UserInfo (if not evicted/expired)
		for _, token := range tokens {
			cache.Set(token, &UserInfo{Username: token})

			got := cache.Get(token)
			if got != nil {
				require.Equal(t, token, got.Username)
			}
		}

		// Property: Cache never exceeds maxSize entries
		cache.mu.Lock()
		assert.LessOrEqual(t, len(cache.entries), maxSize)
		cache.mu.Unlock()

		// Property: After TTL expires, Get returns nil
		time.Sleep(time.Millisecond * 60)
		for _, token := range tokens {
			assert.Nil(t, cache.Get(token))
		}
	})
}
