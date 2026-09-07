package devsession

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	return s
}

func TestAcquire_NewSession(t *testing.T) {
	scheme := newTestScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewSessionManager(client)

	session := DevSession{
		ID:        "sess-1",
		Service:   "payments",
		Namespace: "default",
		Developer: "alice",
		Hostname:  "alice-mac",
		Branch:    "feat/checkout",
	}

	existing, conflicted, err := mgr.Acquire(context.Background(), session, ConflictPolicyWarn, false)
	require.NoError(t, err)
	assert.False(t, conflicted)
	assert.Nil(t, existing)

	sessions, err := mgr.List(context.Background(), "default")
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "payments", sessions[0].Service)
	assert.Equal(t, "alice", sessions[0].Developer)
}

func TestAcquire_SameDeveloper(t *testing.T) {
	scheme := newTestScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewSessionManager(client)

	sess1 := DevSession{
		ID:        "sess-1",
		Service:   "payments",
		Namespace: "default",
		Developer: "alice",
		Hostname:  "alice-mac",
		Branch:    "feat/checkout",
	}
	_, _, err := mgr.Acquire(context.Background(), sess1, ConflictPolicyBlock, false)
	require.NoError(t, err)

	// Alice runs again / restarts
	sess2 := DevSession{
		ID:        "sess-2",
		Service:   "payments",
		Namespace: "default",
		Developer: "alice",
		Hostname:  "alice-mac",
		Branch:    "feat/checkout-v2",
	}
	existing, conflicted, err := mgr.Acquire(context.Background(), sess2, ConflictPolicyBlock, false)
	require.NoError(t, err)
	assert.False(t, conflicted)
	assert.Nil(t, existing)
}

func TestAcquire_Conflict_Warn(t *testing.T) {
	scheme := newTestScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewSessionManager(client)

	// Alice acquires
	sessAlice := DevSession{
		ID:        "sess-alice",
		Service:   "payments",
		Namespace: "default",
		Developer: "alice",
		Hostname:  "alice-mac",
		Branch:    "feat/checkout",
	}
	_, _, err := mgr.Acquire(context.Background(), sessAlice, ConflictPolicyWarn, false)
	require.NoError(t, err)

	// Bob attempts with policy=warn
	sessBob := DevSession{
		ID:        "sess-bob",
		Service:   "payments",
		Namespace: "default",
		Developer: "bob",
		Hostname:  "bobs-laptop",
		Branch:    "fix/discount",
	}
	existing, conflicted, err := mgr.Acquire(context.Background(), sessBob, ConflictPolicyWarn, false)
	require.NoError(t, err)
	assert.True(t, conflicted)
	require.NotNil(t, existing)
	assert.Equal(t, "alice", existing.Developer)

	// Check warning banner generation
	confErr := &ConflictError{
		Service:          "payments",
		ExistingSession:  *existing,
		CurrentDeveloper: "bob",
	}
	warnMsg := confErr.FormatWarning(ConflictPolicyWarn)
	assert.Contains(t, warnMsg, "payments")
	assert.Contains(t, warnMsg, "alice")
	assert.Contains(t, warnMsg, "WARNING")
}

func TestAcquire_Conflict_Block(t *testing.T) {
	scheme := newTestScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewSessionManager(client)

	// Alice acquires
	sessAlice := DevSession{
		ID:        "sess-alice",
		Service:   "payments",
		Namespace: "default",
		Developer: "alice",
		Hostname:  "alice-mac",
		Branch:    "feat/checkout",
	}
	_, _, err := mgr.Acquire(context.Background(), sessAlice, ConflictPolicyWarn, false)
	require.NoError(t, err)

	// Bob attempts with policy=block
	sessBob := DevSession{
		ID:        "sess-bob",
		Service:   "payments",
		Namespace: "default",
		Developer: "bob",
		Hostname:  "bobs-laptop",
		Branch:    "fix/discount",
	}
	existing, conflicted, err := mgr.Acquire(context.Background(), sessBob, ConflictPolicyBlock, false)
	require.Error(t, err)
	assert.True(t, conflicted)
	require.NotNil(t, existing)
	assert.Equal(t, "alice", existing.Developer)

	var ce *ConflictError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, "payments", ce.Service)
	assert.Equal(t, "alice", ce.ExistingSession.Developer)

	blockMsg := ce.FormatWarning(ConflictPolicyBlock)
	assert.Contains(t, blockMsg, "CONFLICT")
	assert.Contains(t, blockMsg, "--force")
}

func TestAcquire_Conflict_ForceOverride(t *testing.T) {
	scheme := newTestScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewSessionManager(client)

	// Alice acquires
	sessAlice := DevSession{
		ID:        "sess-alice",
		Service:   "payments",
		Namespace: "default",
		Developer: "alice",
		Hostname:  "alice-mac",
		Branch:    "feat/checkout",
	}
	_, _, err := mgr.Acquire(context.Background(), sessAlice, ConflictPolicyBlock, false)
	require.NoError(t, err)

	// Bob forces acquisition
	sessBob := DevSession{
		ID:        "sess-bob",
		Service:   "payments",
		Namespace: "default",
		Developer: "bob",
		Hostname:  "bobs-laptop",
		Branch:    "fix/discount",
	}
	existing, conflicted, err := mgr.Acquire(context.Background(), sessBob, ConflictPolicyBlock, true)
	require.NoError(t, err)
	assert.False(t, conflicted)
	assert.Nil(t, existing)

	// Bob now holds session
	sessions, err := mgr.List(context.Background(), "default")
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "bob", sessions[0].Developer)
}

func TestAcquire_StaleSessionReclaimed(t *testing.T) {
	scheme := newTestScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewSessionManager(client)

	// Alice acquired 2 hours ago, heartbeat stopped 10 minutes ago (>90s stale)
	sessAlice := DevSession{
		ID:        "sess-alice",
		Service:   "payments",
		Namespace: "default",
		Developer: "alice",
		Hostname:  "alice-mac",
		Branch:    "feat/checkout",
		StartedAt: time.Now().Add(-2 * time.Hour),
		Heartbeat: time.Now().Add(-10 * time.Minute),
	}
	// Direct write to simulate stale session
	err := mgr.writeSession(context.Background(), sessAlice, nil)
	require.NoError(t, err)

	// Bob attempts with policy=block (would block if active)
	sessBob := DevSession{
		ID:        "sess-bob",
		Service:   "payments",
		Namespace: "default",
		Developer: "bob",
		Hostname:  "bobs-laptop",
		Branch:    "fix/discount",
	}
	existing, conflicted, err := mgr.Acquire(context.Background(), sessBob, ConflictPolicyBlock, false)
	require.NoError(t, err)
	assert.False(t, conflicted)
	assert.Nil(t, existing)

	// Bob now holds session
	sessions, err := mgr.List(context.Background(), "default")
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "bob", sessions[0].Developer)
}

func TestHeartbeatAndRelease(t *testing.T) {
	scheme := newTestScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewSessionManager(client)

	sess := DevSession{
		ID:        "sess-123",
		Service:   "orders",
		Namespace: "default",
		Developer: "carol",
	}
	_, _, err := mgr.Acquire(context.Background(), sess, ConflictPolicyWarn, false)
	require.NoError(t, err)

	// Successful heartbeat
	err = mgr.Heartbeat(context.Background(), "default", "orders", "sess-123")
	require.NoError(t, err)

	// Mismatched session ID heartbeat rejected
	err = mgr.Heartbeat(context.Background(), "default", "orders", "wrong-id")
	require.Error(t, err)

	// Release with matching ID
	err = mgr.Release(context.Background(), "default", "orders", "sess-123")
	require.NoError(t, err)

	// List should now be empty
	sessions, err := mgr.List(context.Background(), "default")
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestList_MultipleServices(t *testing.T) {
	scheme := newTestScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewSessionManager(client)

	s1 := DevSession{ID: "1", Service: "svc-a", Namespace: "default", Developer: "alice"}
	s2 := DevSession{ID: "2", Service: "svc-b", Namespace: "default", Developer: "bob"}

	_, _, err := mgr.Acquire(context.Background(), s1, ConflictPolicyWarn, false)
	require.NoError(t, err)
	_, _, err = mgr.Acquire(context.Background(), s2, ConflictPolicyWarn, false)
	require.NoError(t, err)

	sessions, err := mgr.List(context.Background(), "default")
	require.NoError(t, err)
	assert.Len(t, sessions, 2)
}

func TestDetectHostname(t *testing.T) {
	h := DetectHostname()
	assert.NotEmpty(t, h)
}

func TestConfigMapNameSanitization(t *testing.T) {
	name := ConfigMapNameForService("Payment_Service_V2")
	assert.Equal(t, "diverge-dev-session-payment-service-v2", name)
}
