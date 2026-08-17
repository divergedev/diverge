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
}

// NewTokenCache creates a cache with the given maximum size and entry TTL.
// A maxSize <= 0 creates a no-op cache that never stores entries.
func NewTokenCache(maxSize int, ttl time.Duration) *TokenCache {
	if maxSize <= 0 {
		maxSize = 0
	}
	return &TokenCache{
		entries: make(map[string]*cacheEntry, maxSize),
		order:   make([]string, 0, maxSize),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// Get looks up a cached user identity by token.
// Returns nil if not found or expired. Promotes the entry to newest on hit (LRU).
func (c *TokenCache) Get(token string) *UserInfo {
	key := hashToken(token)
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil
	}

	// Check expiration
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		c.removeFromOrder(key)
		return nil
	}

	// Promote to newest (LRU)
	c.removeFromOrder(key)
	c.order = append(c.order, key)

	return entry.user
}

// Set stores an authenticated user identity keyed by token hash.
// Only cache successful authentications — never cache failures.
// If the key already exists, it is updated in-place without eviction.
func (c *TokenCache) Set(token string, user *UserInfo) {
	key := hashToken(token)
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if key already exists — update in-place
	if _, exists := c.entries[key]; exists {
		c.entries[key] = &cacheEntry{
			user:      user,
			expiresAt: time.Now().Add(c.ttl),
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
		user:      user,
		expiresAt: time.Now().Add(c.ttl),
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
