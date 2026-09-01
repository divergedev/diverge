package webhook

import (
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
