package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/divergedev/diverge/pkg/devsession"
)

func TestPrintDevSessions_Empty(t *testing.T) {
	var buf bytes.Buffer
	printDevSessions(&buf, nil)
	assert.Contains(t, buf.String(), "No active dev sessions found.")
}

func TestPrintDevSessions_Populated(t *testing.T) {
	var buf bytes.Buffer
	now := time.Now()
	sessions := []devsession.DevSession{
		{
			Service:   "payments",
			Developer: "alice",
			Branch:    "feat/checkout",
			Hostname:  "alices-mac",
			Heartbeat: now.Add(-15 * time.Second),
		},
		{
			Service:   "orders",
			Developer: "bob",
			Branch:    "fix/coupon",
			Hostname:  "bobs-laptop",
			Heartbeat: now.Add(-120 * time.Second), // Stale
		},
	}

	printDevSessions(&buf, sessions)
	out := buf.String()
	assert.Contains(t, out, "SERVICE")
	assert.Contains(t, out, "payments")
	assert.Contains(t, out, "alice")
	assert.Contains(t, out, "Active")
	assert.Contains(t, out, "orders")
	assert.Contains(t, out, "bob")
	assert.Contains(t, out, "Stale")
}

func TestPrintDevSessionsJSON_Empty(t *testing.T) {
	var buf bytes.Buffer
	err := printDevSessionsJSON(&buf, nil)
	assert.NoError(t, err)
	assert.Equal(t, "[]\n", buf.String())
}

func TestPrintDevSessionsJSON_Populated(t *testing.T) {
	var buf bytes.Buffer
	now := time.Now()
	sessions := []devsession.DevSession{
		{
			Service:   "payments",
			Developer: "alice",
			Branch:    "feat/checkout",
			Hostname:  "alices-mac",
			Heartbeat: now.Add(-15 * time.Second),
		},
		{
			Service:   "orders",
			Developer: "bob",
			Branch:    "fix/coupon",
			Hostname:  "bobs-laptop",
			Heartbeat: now.Add(-120 * time.Second), // Stale
		},
	}

	err := printDevSessionsJSON(&buf, sessions)
	assert.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, `"service": "payments"`)
	assert.Contains(t, out, `"developer": "alice"`)
	assert.Contains(t, out, `"status": "ACTIVE"`)
	assert.Contains(t, out, `"service": "orders"`)
	assert.Contains(t, out, `"developer": "bob"`)
	assert.Contains(t, out, `"status": "STALE"`)
}
