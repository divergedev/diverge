package webhook

import (
	"sort"
	"sync"
	"time"
)

const (
	maxDeliveryIDs = 10000
	deliveryIDTTL  = 5 * time.Minute
)

// DeliveryDedup prevents webhook replay attacks by tracking delivery IDs.
type DeliveryDedup struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

func NewDeliveryDedup() *DeliveryDedup {
	return &DeliveryDedup{
		entries: make(map[string]time.Time),
	}
}

func (d *DeliveryDedup) IsDuplicate(id string) bool {
	if id == "" {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	// Check if exists and not expired
	if ts, exists := d.entries[id]; exists {
		if now.Sub(ts) < deliveryIDTTL {
			return true
		}
		// expired
		delete(d.entries, id)
	}

	// Insert
	if len(d.entries) >= maxDeliveryIDs {
		// Evict expired
		for k, v := range d.entries {
			if now.Sub(v) >= deliveryIDTTL {
				delete(d.entries, k)
			}
		}
		// If still full, evict oldest 10%
		if len(d.entries) >= maxDeliveryIDs {
			type dedupEntry struct {
				key string
				ts  time.Time
			}
			entriesList := make([]dedupEntry, 0, len(d.entries))
			for k, v := range d.entries {
				entriesList = append(entriesList, dedupEntry{key: k, ts: v})
			}
			sort.Slice(entriesList, func(i, j int) bool {
				return entriesList[i].ts.Before(entriesList[j].ts)
			})
			evictCount := maxDeliveryIDs / 10
			for i := 0; i < evictCount && i < len(entriesList); i++ {
				delete(d.entries, entriesList[i].key)
			}
		}
	}

	d.entries[id] = now
	return false
}
