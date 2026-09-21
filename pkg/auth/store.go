package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// ErrTokenNotFound is returned when a requested capability token does not exist in the store.
	ErrTokenNotFound = errors.New("token not found")
	// ErrTokenRevoked is returned when attempting to retrieve or verify a revoked capability token.
	ErrTokenRevoked = errors.New("token has been revoked")
)

const (
	// LabelAgentTask is the metadata label identifying the associated AgentTask.
	LabelAgentTask = "divergedev.com/agent-task"
	// LabelRevoked is the metadata label marking a token secret as revoked.
	LabelRevoked = "divergedev.com/revoked"
	// ManagedByLabel is the standard Kubernetes management label.
	ManagedByLabel = "app.kubernetes.io/managed-by"
)

// InMemoryTokenStore provides an in-memory, thread-safe implementation of TokenStore.
// Ideal for unit tests, local CLI mock mode, and standalone testing.
type InMemoryTokenStore struct {
	mu      sync.RWMutex
	tokens  map[string][]byte
	revoked map[string]bool
}

var _ TokenStore = (*InMemoryTokenStore)(nil)

// NewInMemoryTokenStore initializes a thread-safe InMemoryTokenStore.
func NewInMemoryTokenStore() *InMemoryTokenStore {
	return &InMemoryTokenStore{
		tokens:  make(map[string][]byte),
		revoked: make(map[string]bool),
	}
}

func tokenKey(taskID, namespace string) string {
	return fmt.Sprintf("%s/%s", namespace, taskID)
}

// SaveToken persists the raw token bytes.
func (s *InMemoryTokenStore) SaveToken(_ context.Context, taskID, namespace string, token []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tokenKey(taskID, namespace)
	s.tokens[key] = append([]byte(nil), token...)
	delete(s.revoked, key)
	return nil
}

// GetToken retrieves the token or returns ErrTokenNotFound/ErrTokenRevoked.
func (s *InMemoryTokenStore) GetToken(_ context.Context, taskID, namespace string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := tokenKey(taskID, namespace)
	if s.revoked[key] {
		return nil, ErrTokenRevoked
	}
	tok, ok := s.tokens[key]
	if !ok {
		return nil, ErrTokenNotFound
	}
	return append([]byte(nil), tok...), nil
}

// DeleteToken removes the token from storage.
func (s *InMemoryTokenStore) DeleteToken(_ context.Context, taskID, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tokenKey(taskID, namespace)
	delete(s.tokens, key)
	delete(s.revoked, key)
	return nil
}

// RevokeToken marks the token as revoked.
func (s *InMemoryTokenStore) RevokeToken(_ context.Context, taskID, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tokenKey(taskID, namespace)
	s.revoked[key] = true
	return nil
}

// IsRevoked checks if the token has been marked revoked.
func (s *InMemoryTokenStore) IsRevoked(_ context.Context, taskID, namespace string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revoked[tokenKey(taskID, namespace)]
}

// KubernetesSecretTokenStore implements TokenStore using Kubernetes corev1.Secret resources.
type KubernetesSecretTokenStore struct {
	client client.Client
	scheme *runtime.Scheme
}

var _ TokenStore = (*KubernetesSecretTokenStore)(nil)

// NewKubernetesSecretTokenStore initializes a KubernetesSecretTokenStore.
func NewKubernetesSecretTokenStore(c client.Client, scheme *runtime.Scheme) *KubernetesSecretTokenStore {
	return &KubernetesSecretTokenStore{
		client: c,
		scheme: scheme,
	}
}

func secretName(taskID string) string {
	return fmt.Sprintf("%s-token", taskID)
}

// SaveToken creates or updates the <task-name>-token Kubernetes Secret.
func (s *KubernetesSecretTokenStore) SaveToken(ctx context.Context, taskID, namespace string, token []byte) error {
	name := secretName(taskID)
	sec := &corev1.Secret{}
	err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sec)
	if err != nil {
		if apierrors.IsNotFound(err) {
			sec = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
					Labels: map[string]string{
						LabelAgentTask: taskID,
						ManagedByLabel: "diverge",
					},
				},
				Data: map[string][]byte{
					"token": token,
				},
			}
			return s.client.Create(ctx, sec)
		}
		return fmt.Errorf("get token secret %s: %w", name, err)
	}

	if sec.Data == nil {
		sec.Data = make(map[string][]byte)
	}
	sec.Data["token"] = token
	if sec.Labels != nil {
		delete(sec.Labels, LabelRevoked)
	}
	return s.client.Update(ctx, sec)
}

// GetToken reads the token from the Kubernetes Secret.
func (s *KubernetesSecretTokenStore) GetToken(ctx context.Context, taskID, namespace string) ([]byte, error) {
	name := secretName(taskID)
	sec := &corev1.Secret{}
	err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sec)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrTokenNotFound
		}
		return nil, fmt.Errorf("get token secret %s: %w", name, err)
	}

	if sec.Labels != nil && sec.Labels[LabelRevoked] == "true" {
		return nil, ErrTokenRevoked
	}

	tok, ok := sec.Data["token"]
	if !ok || len(tok) == 0 {
		return nil, ErrTokenNotFound
	}
	return tok, nil
}

// DeleteToken removes the token Secret.
func (s *KubernetesSecretTokenStore) DeleteToken(ctx context.Context, taskID, namespace string) error {
	name := secretName(taskID)
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	err := s.client.Delete(ctx, sec)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete token secret %s: %w", name, err)
	}
	return nil
}

// RevokeToken marks the token Secret as revoked via label and data entry.
func (s *KubernetesSecretTokenStore) RevokeToken(ctx context.Context, taskID, namespace string) error {
	name := secretName(taskID)
	sec := &corev1.Secret{}
	err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sec)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ErrTokenNotFound
		}
		return fmt.Errorf("get token secret %s for revocation: %w", name, err)
	}

	if sec.Labels == nil {
		sec.Labels = make(map[string]string)
	}
	sec.Labels[LabelRevoked] = "true"
	return s.client.Update(ctx, sec)
}

// IsRevoked inspects whether the token Secret has the revoked label.
func (s *KubernetesSecretTokenStore) IsRevoked(ctx context.Context, taskID, namespace string) bool {
	name := secretName(taskID)
	sec := &corev1.Secret{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sec); err != nil {
		return false
	}
	return sec.Labels != nil && sec.Labels[LabelRevoked] == "true"
}

// NoopTokenAuditor discards audit events.
type NoopTokenAuditor struct{}

var _ TokenAuditor = (*NoopTokenAuditor)(nil)

func (a *NoopTokenAuditor) RecordEvent(_ context.Context, _ AuthEvent) error {
	return nil
}

// LoggerTokenAuditor logs audit events to structured logger.
type LoggerTokenAuditor struct {
	logger *slog.Logger
}

var _ TokenAuditor = (*LoggerTokenAuditor)(nil)

// NewLoggerTokenAuditor constructs a LoggerTokenAuditor.
func NewLoggerTokenAuditor(logger *slog.Logger) *LoggerTokenAuditor {
	if logger == nil {
		logger = slog.Default()
	}
	return &LoggerTokenAuditor{logger: logger}
}

// RecordEvent logs an AuthEvent.
func (a *LoggerTokenAuditor) RecordEvent(_ context.Context, event AuthEvent) error {
	a.logger.Info("auth capability event",
		"type", event.Type,
		"task_id", event.TaskID,
		"namespace", event.Namespace,
		"principal", event.Principal,
		"success", event.Success,
		"reason", event.Reason,
		"timestamp", event.Timestamp,
	)
	return nil
}
