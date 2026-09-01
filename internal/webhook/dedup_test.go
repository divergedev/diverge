package webhook

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDeliveryDedup_FirstSeen(t *testing.T) {
	dedup := NewDeliveryDedup()
	assert.False(t, dedup.IsDuplicate("id1"))
	assert.False(t, dedup.IsDuplicate("id2"))
}

func TestDeliveryDedup_Duplicate(t *testing.T) {
	dedup := NewDeliveryDedup()
	assert.False(t, dedup.IsDuplicate("id1"))
	assert.True(t, dedup.IsDuplicate("id1"))
}

func TestDeliveryDedup_Expired(t *testing.T) {
	dedup := NewDeliveryDedup()
	assert.False(t, dedup.IsDuplicate("id1"))

	// Fast forward time
	dedup.mu.Lock()
	dedup.entries["id1"] = time.Now().Add(-10 * time.Minute)
	dedup.mu.Unlock()

	// Should not be duplicate since it expired
	assert.False(t, dedup.IsDuplicate("id1"))
}

func TestDeliveryDedup_Eviction(t *testing.T) {
	dedup := NewDeliveryDedup()

	// Fill it up
	for i := 0; i < maxDeliveryIDs; i++ {
		dedup.entries[string(rune(i))] = time.Now()
	}

	assert.Len(t, dedup.entries, maxDeliveryIDs)

	// This should trigger eviction
	assert.False(t, dedup.IsDuplicate("new-id"))
	assert.LessOrEqual(t, len(dedup.entries), maxDeliveryIDs)
}

func TestDeliveryDedup_EvictionOrder(t *testing.T) {
	dedup := NewDeliveryDedup()
	now := time.Now()

	dedup.mu.Lock()
	for i := 0; i < maxDeliveryIDs; i++ {
		// Oldest at i=0, newest at i=maxDeliveryIDs-1
		dedup.entries[strconv.Itoa(i)] = now.Add(time.Duration(i) * time.Millisecond)
	}
	dedup.mu.Unlock()

	// Add one more to trigger eviction (evicts oldest 10% which is 1000 items)
	assert.False(t, dedup.IsDuplicate("trigger-eviction"))

	dedup.mu.Lock()
	defer dedup.mu.Unlock()

	// Verify total length is less or equal to maxDeliveryIDs
	assert.LessOrEqual(t, len(dedup.entries), maxDeliveryIDs)

	// The oldest 10% (1000 items) should be evicted
	for i := 0; i < maxDeliveryIDs/10; i++ {
		_, exists := dedup.entries[strconv.Itoa(i)]
		assert.False(t, exists, "oldest entry %d should be evicted", i)
	}

	// The newest ones should remain
	_, exists := dedup.entries[strconv.Itoa(maxDeliveryIDs-1)]
	assert.True(t, exists, "newest entry should still exist")
}
