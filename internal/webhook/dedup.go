package webhook

import (
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
			toRemove := maxDeliveryIDs / 10
			for k := range d.entries {
				delete(d.entries, k)
				toRemove--
				if toRemove <= 0 {
					break
				}
			}
		}
	}

	d.entries[id] = now
	return false
}
