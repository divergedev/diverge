package devsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConflictPolicy defines how to handle active developer collisions.
type ConflictPolicy string

const (
	// ConflictPolicyWarn logs a prominent warning but proceeds with the session.
	ConflictPolicyWarn ConflictPolicy = "warn"
	// ConflictPolicyBlock prevents the session from starting unless forced.
	ConflictPolicyBlock ConflictPolicy = "block"
	// ConflictPolicyAllow overwrites the existing session (last-writer-wins).
	ConflictPolicyAllow ConflictPolicy = "allow"
)

// StaleSessionDuration defines the duration after which an un-renewed session is considered abandoned.
// Aligns with Diverge's 90-second lease expiration.
const StaleSessionDuration = 90 * time.Second

// Labels and keys for ConfigMap tracking.
const (
	ConfigMapPrefix   = "diverge-dev-session-"
	ConfigMapDataKey  = "session"
	LabelManagedBy    = "divergedev.com/managed-by"
	LabelManagedByVal = "diverge"
	LabelService      = "divergedev.com/service"
	LabelDeveloper    = "divergedev.com/developer"
	LabelSessionID    = "divergedev.com/session-id"
)

var (
	ErrConflict        = errors.New("dev session conflict detected")
	ErrSessionNotFound = errors.New("dev session not found")
)

// DevSession contains metadata for an active local development session.
type DevSession struct {
	ID        string    `json:"id"`
	Service   string    `json:"service"`
	Namespace string    `json:"namespace"`
	Developer string    `json:"developer"`
	Hostname  string    `json:"hostname"`
	Branch    string    `json:"branch"`
	PreviewID string    `json:"previewId"`
	GroupName string    `json:"groupName"`
	StartedAt time.Time `json:"startedAt"`
	Heartbeat time.Time `json:"heartbeat"`
	PID       int       `json:"pid,omitempty"`
}

// IsStale returns true if the session heartbeat has not been updated within StaleSessionDuration.
func (s DevSession) IsStale(now time.Time) bool {
	if s.Heartbeat.IsZero() {
		return true
	}
	return now.Sub(s.Heartbeat) > StaleSessionDuration
}

// ConflictError represents a collision with another active developer session.
type ConflictError struct {
	Service          string
	ExistingSession  DevSession
	CurrentDeveloper string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflict: service %q is currently locked by developer %q on machine %q (last heartbeat: %s ago)",
		e.Service, e.ExistingSession.Developer, e.ExistingSession.Hostname,
		time.Since(e.ExistingSession.Heartbeat).Round(time.Second))
}

// FormatWarning returns a prominent, human-readable terminal warning banner.
func (e *ConflictError) FormatWarning(policy ConflictPolicy) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("┌────────────────────────────────────────────────────────────────────────┐\n")
	if policy == ConflictPolicyBlock {
		b.WriteString("│ ❌ CONFLICT: Service is locked by another developer!                    │\n")
	} else {
		b.WriteString("│ ⚠️  WARNING: Another developer is currently working on this service!    │\n")
	}
	b.WriteString("├────────────────────────────────────────────────────────────────────────┤\n")
	fmt.Fprintf(&b, "│  Service:        %-53s │\n", e.Service)
	fmt.Fprintf(&b, "│  Active User:    %-53s │\n", e.ExistingSession.Developer)
	fmt.Fprintf(&b, "│  Machine / Host: %-53s │\n", e.ExistingSession.Hostname)
	fmt.Fprintf(&b, "│  Branch:         %-53s │\n", e.ExistingSession.Branch)
	fmt.Fprintf(&b, "│  Preview ID:     %-53s │\n", e.ExistingSession.PreviewID)
	fmt.Fprintf(&b, "│  Session Age:    %-53s │\n", time.Since(e.ExistingSession.StartedAt).Round(time.Minute))
	fmt.Fprintf(&b, "│  Last Heartbeat: %-53s │\n", fmt.Sprintf("%s ago", time.Since(e.ExistingSession.Heartbeat).Round(time.Second)))
	b.WriteString("├────────────────────────────────────────────────────────────────────────┤\n")
	if policy == ConflictPolicyBlock {
		b.WriteString("│ Action: Dev session blocked.                                           │\n")
		b.WriteString("│ To override and take over the lock, run with:                          │\n")
		fmt.Fprintf(&b, "│   diverge dev --service %-37s --force   │\n", e.Service)
	} else {
		b.WriteString("│ Policy: Warn only (--on-conflict=warn). Proceeding with session.       │\n")
		b.WriteString("│ ⚠️  Note: Concurrent mutations may collide on shared baseline state.   │\n")
	}
	b.WriteString("└────────────────────────────────────────────────────────────────────────┘\n")
	return b.String()
}

// ConfigMapNameForService returns the standard ConfigMap name for tracking a service's dev session.
func ConfigMapNameForService(service string) string {
	// Sanitize service name to be DNS-1123 compliant
	clean := strings.ToLower(service)
	clean = strings.ReplaceAll(clean, "_", "-")
	return ConfigMapPrefix + clean
}

// Manager defines the interface for distributed developer session coordination.
type Manager interface {
	// Acquire attempts to start a session. Returns (existingSession, wasConflict, error).
	Acquire(ctx context.Context, session DevSession, policy ConflictPolicy, force bool) (*DevSession, bool, error)
	// Heartbeat extends the active session's lease.
	Heartbeat(ctx context.Context, namespace, service, sessionID string) error
	// Release cleanly tears down the session.
	Release(ctx context.Context, namespace, service, sessionID string) error
	// List returns all active dev sessions in the namespace.
	List(ctx context.Context, namespace string) ([]DevSession, error)
}

// K8sSessionManager implements Manager using Kubernetes ConfigMaps.
type K8sSessionManager struct {
	client client.Client
}

// NewSessionManager creates a new K8sSessionManager.
func NewSessionManager(c client.Client) *K8sSessionManager {
	return &K8sSessionManager{client: c}
}

// Acquire registers the developer's session, checking for collisions with other developers.
func (m *K8sSessionManager) Acquire(ctx context.Context, session DevSession, policy ConflictPolicy, force bool) (*DevSession, bool, error) {
	if session.Service == "" {
		return nil, false, fmt.Errorf("session service cannot be empty")
	}
	if session.Namespace == "" {
		session.Namespace = "default"
	}
	now := time.Now()
	if session.StartedAt.IsZero() {
		session.StartedAt = now
	}
	session.Heartbeat = now

	cmName := ConfigMapNameForService(session.Service)
	var existingCM corev1.ConfigMap
	err := m.client.Get(ctx, types.NamespacedName{Name: cmName, Namespace: session.Namespace}, &existingCM)

	if err != nil && !apierrors.IsNotFound(err) {
		return nil, false, fmt.Errorf("checking existing dev session: %w", err)
	}

	if err == nil {
		// Existing session found. Parse it.
		var existing DevSession
		dataStr := existingCM.Data[ConfigMapDataKey]
		if parseErr := json.Unmarshal([]byte(dataStr), &existing); parseErr == nil {
			isSameDeveloper := (existing.Developer == session.Developer)
			isStale := existing.IsStale(now)

			// If it's a DIFFERENT developer and NOT stale and NOT force-allowed:
			if !isSameDeveloper && !isStale && !force && policy != ConflictPolicyAllow {
				conflictErr := &ConflictError{
					Service:          session.Service,
					ExistingSession:  existing,
					CurrentDeveloper: session.Developer,
				}

				if policy == ConflictPolicyBlock {
					return &existing, true, conflictErr
				}
				// Policy is warn: record conflict flag, but overwrite session lock
				if writeErr := m.writeSession(ctx, session, &existingCM); writeErr != nil {
					return nil, false, writeErr
				}
				return &existing, true, nil
			}
		}

		// Update existing ConfigMap
		if writeErr := m.writeSession(ctx, session, &existingCM); writeErr != nil {
			return nil, false, writeErr
		}
		return nil, false, nil
	}

	// Not found: create new ConfigMap
	if writeErr := m.writeSession(ctx, session, nil); writeErr != nil {
		return nil, false, writeErr
	}

	return nil, false, nil
}

// Heartbeat refreshes the session's heartbeat timestamp.
func (m *K8sSessionManager) Heartbeat(ctx context.Context, namespace, service, sessionID string) error {
	if namespace == "" {
		namespace = "default"
	}
	cmName := ConfigMapNameForService(service)
	var cm corev1.ConfigMap
	if err := m.client.Get(ctx, types.NamespacedName{Name: cmName, Namespace: namespace}, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return ErrSessionNotFound
		}
		return err
	}

	var session DevSession
	if err := json.Unmarshal([]byte(cm.Data[ConfigMapDataKey]), &session); err != nil {
		return fmt.Errorf("unmarshaling session: %w", err)
	}

	// Only update if session ID matches (ensuring we don't renew someone else's stolen session)
	if session.ID != sessionID {
		return fmt.Errorf("session ID mismatch (held by %s, our ID is %s)", session.ID, sessionID)
	}

	session.Heartbeat = time.Now()
	return m.writeSession(ctx, session, &cm)
}

// Release cleanly deletes the ConfigMap if held by sessionID.
func (m *K8sSessionManager) Release(ctx context.Context, namespace, service, sessionID string) error {
	if namespace == "" {
		namespace = "default"
	}
	cmName := ConfigMapNameForService(service)
	var cm corev1.ConfigMap
	if err := m.client.Get(ctx, types.NamespacedName{Name: cmName, Namespace: namespace}, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return nil // already clean
		}
		return err
	}

	var session DevSession
	if err := json.Unmarshal([]byte(cm.Data[ConfigMapDataKey]), &session); err == nil {
		if sessionID != "" && session.ID != sessionID {
			// Held by another session (stolen or restarted); do not delete someone else's lock
			return nil
		}
	}

	if err := m.client.Delete(ctx, &cm); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting dev session configmap: %w", err)
	}
	return nil
}

// List returns all active dev sessions in the given namespace.
func (m *K8sSessionManager) List(ctx context.Context, namespace string) ([]DevSession, error) {
	if namespace == "" {
		namespace = "default"
	}
	var cmList corev1.ConfigMapList
	opts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels{
			LabelManagedBy: LabelManagedByVal,
		},
	}
	if err := m.client.List(ctx, &cmList, opts...); err != nil {
		return nil, fmt.Errorf("listing session configmaps: %w", err)
	}

	sessions := make([]DevSession, 0, len(cmList.Items))
	for _, cm := range cmList.Items {
		if dataStr, ok := cm.Data[ConfigMapDataKey]; ok {
			var s DevSession
			if err := json.Unmarshal([]byte(dataStr), &s); err == nil {
				sessions = append(sessions, s)
			}
		}
	}
	return sessions, nil
}

func (m *K8sSessionManager) writeSession(ctx context.Context, session DevSession, existing *corev1.ConfigMap) error {
	payload, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}

	cmName := ConfigMapNameForService(session.Service)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: session.Namespace,
			Labels: map[string]string{
				LabelManagedBy: LabelManagedByVal,
				LabelService:   session.Service,
				LabelDeveloper: session.Developer,
				LabelSessionID: session.ID,
			},
		},
		Data: map[string]string{
			ConfigMapDataKey: string(payload),
		},
	}

	if existing != nil {
		existing.Labels = cm.Labels
		existing.Data = cm.Data
		return m.client.Update(ctx, existing)
	}

	return m.client.Create(ctx, cm)
}

// DetectHostname returns the local machine hostname.
func DetectHostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "localhost"
	}
	return h
}
