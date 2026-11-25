package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type cacheEntry struct {
	user      *UserInfo
	expiresAt time.Time
}

// TokenCache is a bounded LRU cache for authenticated TokenReview results.
// Keys are SHA-256 hashes of tokens — raw tokens are never stored.
type TokenCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
	order   []string // LRU order: oldest at front, newest at end
	maxSize int
	ttl     time.Duration
	now     func() time.Time
}

// NewTokenCache creates a cache with the given maximum size and entry TTL.
// A maxSize <= 0 creates a no-op cache that never stores entries.
func NewTokenCache(maxSize int, ttl time.Duration) *TokenCache {
	return newTokenCacheWithClock(maxSize, ttl, time.Now)
}

func newTokenCacheWithClock(maxSize int, ttl time.Duration, now func() time.Time) *TokenCache {
	if maxSize <= 0 {
		maxSize = 0
	}
	if now == nil {
		now = time.Now
	}
	return &TokenCache{
		entries: make(map[string]*cacheEntry, maxSize),
		order:   make([]string, 0, maxSize),
		maxSize: maxSize,
		ttl:     ttl,
		now:     now,
	}
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// Get looks up a cached user identity by token.
// Returns nil if not found or expired. Promotes the entry to newest on hit (LRU).
// Always returns a DeepCopy so callers cannot mutate cached state.
func (c *TokenCache) Get(token string) *UserInfo {
	if c.maxSize <= 0 {
		return nil
	}
	key := hashToken(token)
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil
	}

	now := c.now()
	// Check cache TTL and underlying token expiration
	if now.After(entry.expiresAt) || (entry.user != nil && !entry.user.ExpiresAt.IsZero() && now.After(entry.user.ExpiresAt)) {
		delete(c.entries, key)
		c.removeFromOrder(key)
		return nil
	}

	// Promote to newest (LRU)
	c.removeFromOrder(key)
	c.order = append(c.order, key)

	return entry.user.DeepCopy()
}

// Set stores an authenticated user identity keyed by token hash.
// Only cache successful authentications — never cache failures.
// If the key already exists, it is updated in-place without eviction.
func (c *TokenCache) Set(token string, user *UserInfo) {
	if c.maxSize <= 0 || user == nil {
		return
	}
	now := c.now()
	exp := now.Add(c.ttl)
	if !user.ExpiresAt.IsZero() {
		if !user.ExpiresAt.After(now) {
			return
		}
		if user.ExpiresAt.Before(exp) {
			exp = user.ExpiresAt
		}
	}

	stored := user.DeepCopy()
	stored.FromCookie = false

	key := hashToken(token)
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if key already exists — update in-place
	if _, exists := c.entries[key]; exists {
		c.entries[key] = &cacheEntry{
			user:      stored,
			expiresAt: exp,
		}
		// Promote to newest
		c.removeFromOrder(key)
		c.order = append(c.order, key)
		return
	}

	// Evict oldest if at capacity
	if len(c.entries) >= c.maxSize {
		if len(c.order) > 0 {
			oldest := c.order[0]
			delete(c.entries, oldest)
			c.order = c.order[1:]
		}
	}

	c.entries[key] = &cacheEntry{
		user:      stored,
		expiresAt: exp,
	}
	c.order = append(c.order, key)
}

func (c *TokenCache) removeFromOrder(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}
