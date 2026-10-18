package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// ErrTokenNotFound is returned when a requested capability token does not exist in the store.
	ErrTokenNotFound = errors.New("token not found")
	// ErrTokenRevoked is returned when attempting to retrieve or verify a revoked capability token.
	ErrTokenRevoked = errors.New("token has been revoked")

	// indexRetryBackoff configures conflict retry steps for the shared per-namespace token index Secret.
	indexRetryBackoff = wait.Backoff{
		Steps:    25,
		Duration: 2 * time.Millisecond,
		Factor:   1.2,
		Jitter:   0.2,
	}
)

const (
	// LabelAgentTask is the metadata label identifying the associated AgentTask.
	LabelAgentTask = "divergedev.com/agent-task"
	// LabelRevoked is the metadata label marking a token secret as revoked.
	LabelRevoked = "divergedev.com/revoked"
	// ManagedByLabel is the standard Kubernetes management label.
	ManagedByLabel = "app.kubernetes.io/managed-by"
	// TokenIndexSecretName is the fixed Secret name per namespace used to index active
	// AgentTask capability tokens so diverge-server RBAC can restrict Secret reads via resourceNames.
	TokenIndexSecretName = "diverge-agenttask-tokens"
)

func isAlnumASCII(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func isLabelCharASCII(b byte) bool {
	return isAlnumASCII(b) || b == '-' || b == '_' || b == '.'
}

func isValidK8sLabel(s string) bool {
	if len(s) == 0 || len(s) > 63 {
		return false
	}
	if !isAlnumASCII(s[0]) || !isAlnumASCII(s[len(s)-1]) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isLabelCharASCII(s[i]) {
			return false
		}
	}
	return true
}

// TaskLabelValue returns a Kubernetes-valid label value (<= 63 chars) for an AgentTask identifier.
func TaskLabelValue(taskID string) string {
	if isValidK8sLabel(taskID) {
		return taskID
	}
	h := sha256.Sum256([]byte(taskID))
	hashStr := hex.EncodeToString(h[:8]) // 16 hex chars
	maxPrefix := 63 - 1 - len(hashStr)

	var b strings.Builder
	b.Grow(len(taskID))
	for i := 0; i < len(taskID); i++ {
		if isLabelCharASCII(taskID[i]) {
			b.WriteByte(taskID[i])
		} else {
			b.WriteByte('-')
		}
	}
	prefix := b.String()
	if len(prefix) > maxPrefix {
		prefix = prefix[:maxPrefix]
	}
	prefix = strings.TrimLeft(prefix, "-_.")
	prefix = strings.TrimRight(prefix, "-_.")
	if prefix == "" {
		return hashStr
	}
	return fmt.Sprintf("%s-%s", prefix, hashStr)
}

func legacyTaskLabelValue(taskID string) string {
	if len(taskID) <= 63 {
		return taskID
	}
	h := sha256.Sum256([]byte(taskID))
	hashStr := hex.EncodeToString(h[:8])
	return fmt.Sprintf("%s-%s", taskID[:63-1-len(hashStr)], hashStr)
}

func matchesTaskLabel(labels map[string]string, taskID string) bool {
	if labels == nil {
		return false
	}
	actual := labels[LabelAgentTask]
	if actual == "" {
		return false
	}
	return actual == TaskLabelValue(taskID) || actual == legacyTaskLabelValue(taskID)
}

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

// tokenKey builds the namespace-scoped map key for in-memory token storage.
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
	client    client.Client
	scheme    *runtime.Scheme
	indexOnly bool
	indexMu   sync.Mutex
}

var _ TokenStore = (*KubernetesSecretTokenStore)(nil)

// NewKubernetesSecretTokenStore initializes a KubernetesSecretTokenStore.
func NewKubernetesSecretTokenStore(c client.Client, scheme *runtime.Scheme) *KubernetesSecretTokenStore {
	return &KubernetesSecretTokenStore{
		client: c,
		scheme: scheme,
	}
}

// WithIndexOnly configures the store to read token state exclusively from the
// namespace-scoped TokenIndexSecretName ("diverge-agenttask-tokens") Secret, allowing
// callers like diverge-server to operate with RBAC restricted via resourceNames.
func (s *KubernetesSecretTokenStore) WithIndexOnly() *KubernetesSecretTokenStore {
	s.indexOnly = true
	return s
}

// SecretName returns the deterministic Kubernetes Secret name for an AgentTask capability token.
func SecretName(taskID string) string {
	base := fmt.Sprintf("%s-token", taskID)
	if len(base) <= 253 {
		return base
	}
	h := sha256.Sum256([]byte(taskID))
	hashStr := hex.EncodeToString(h[:8]) // 16 hex chars
	prefixLen := 253 - len("-token-") - len(hashStr)
	return fmt.Sprintf("%s-token-%s", taskID[:prefixLen], hashStr)
}

// secretName delegates to SecretName for internal callers.
func secretName(taskID string) string {
	return SecretName(taskID)
}

// revokedIndexKey returns the annotation key used in TokenIndexSecretName to record revocation.
func revokedIndexKey(taskID string) string {
	h := sha256.Sum256([]byte(taskID))
	return "revoked.divergedev.com/" + hex.EncodeToString(h[:16])
}

// SaveToken creates or updates the <task-name>-token Kubernetes Secret and syncs the namespace token index.
func (s *KubernetesSecretTokenStore) SaveToken(ctx context.Context, taskID, namespace string, token []byte) error {
	name := SecretName(taskID)
	labelVal := TaskLabelValue(taskID)
	sec := &corev1.Secret{}
	err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sec)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("get token secret %s: %w", name, err)
		}
		sec = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels: map[string]string{
					LabelAgentTask: labelVal,
					ManagedByLabel: "diverge",
				},
			},
			Data: map[string][]byte{
				"token": token,
			},
		}
		if err := s.client.Create(ctx, sec); err != nil {
			return err
		}
		return s.upsertTokenIndex(ctx, taskID, namespace, token)
	}

	if !matchesTaskLabel(sec.Labels, taskID) {
		return fmt.Errorf("secret %s is not owned by agent task %s", name, taskID)
	}
	sec.Labels[LabelAgentTask] = labelVal

	if sec.Data == nil {
		sec.Data = make(map[string][]byte)
	}
	sec.Data["token"] = token
	if sec.Labels != nil {
		delete(sec.Labels, LabelRevoked)
	}
	if err := s.client.Update(ctx, sec); err != nil {
		return err
	}
	return s.upsertTokenIndex(ctx, taskID, namespace, token)
}

// GetToken reads the token from the Kubernetes Secret (or from TokenIndexSecretName when indexOnly is enabled).
func (s *KubernetesSecretTokenStore) GetToken(ctx context.Context, taskID, namespace string) ([]byte, error) {
	if s.indexOnly {
		return s.getTokenFromIndex(ctx, taskID, namespace)
	}
	name := SecretName(taskID)
	sec := &corev1.Secret{}
	err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sec)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrTokenNotFound
		}
		return nil, fmt.Errorf("get token secret %s: %w", name, err)
	}

	if !matchesTaskLabel(sec.Labels, taskID) {
		return nil, ErrTokenNotFound
	}

	if sec.Labels != nil && sec.Labels[LabelRevoked] == "true" {
		return nil, ErrTokenRevoked
	}
	if s.isRevokedInIndex(ctx, taskID, namespace) {
		return nil, ErrTokenRevoked
	}

	tok, ok := sec.Data["token"]
	if !ok || len(tok) == 0 {
		return nil, ErrTokenNotFound
	}
	return tok, nil
}

// isRevokedInIndex checks whether TokenIndexSecretName records taskID as revoked.
func (s *KubernetesSecretTokenStore) isRevokedInIndex(ctx context.Context, taskID, namespace string) bool {
	sec := &corev1.Secret{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: TokenIndexSecretName}, sec); err != nil {
		return false
	}
	return sec.Annotations != nil && sec.Annotations[revokedIndexKey(taskID)] == "true"
}

// getTokenFromIndex reads a task's active or revoked token state from TokenIndexSecretName.
func (s *KubernetesSecretTokenStore) getTokenFromIndex(ctx context.Context, taskID, namespace string) ([]byte, error) {
	sec := &corev1.Secret{}
	err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: TokenIndexSecretName}, sec)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrTokenNotFound
		}
		return nil, fmt.Errorf("get token index secret %s: %w", TokenIndexSecretName, err)
	}
	if sec.Annotations != nil && sec.Annotations[revokedIndexKey(taskID)] == "true" {
		return nil, ErrTokenRevoked
	}
	tok, ok := sec.Data[taskID]
	if !ok || len(tok) == 0 {
		return nil, ErrTokenNotFound
	}
	return tok, nil
}

// DeleteToken removes the token Secret and clears its entry in TokenIndexSecretName.
func (s *KubernetesSecretTokenStore) DeleteToken(ctx context.Context, taskID, namespace string) error {
	name := SecretName(taskID)
	sec := &corev1.Secret{}
	err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sec)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return s.removeFromTokenIndex(ctx, taskID, namespace)
		}
		return fmt.Errorf("get token secret %s: %w", name, err)
	}

	if !matchesTaskLabel(sec.Labels, taskID) {
		return fmt.Errorf("secret %s is not owned by agent task %s", name, taskID)
	}

	if err := s.client.Delete(ctx, sec); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete token secret %s: %w", name, err)
	}
	return s.removeFromTokenIndex(ctx, taskID, namespace)
}

// RevokeToken marks the token Secret and TokenIndexSecretName entry as revoked.
func (s *KubernetesSecretTokenStore) RevokeToken(ctx context.Context, taskID, namespace string) error {
	if s.indexOnly {
		return s.markRevokedInIndex(ctx, taskID, namespace)
	}

	name := SecretName(taskID)
	labelVal := TaskLabelValue(taskID)
	sec := &corev1.Secret{}
	err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sec)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ErrTokenNotFound
		}
		return fmt.Errorf("get token secret %s for revocation: %w", name, err)
	}

	if !matchesTaskLabel(sec.Labels, taskID) {
		return fmt.Errorf("secret %s is not owned by agent task %s", name, taskID)
	}

	sec.Labels[LabelAgentTask] = labelVal
	sec.Labels[LabelRevoked] = "true"
	if err := s.client.Update(ctx, sec); err != nil {
		return err
	}
	return s.markRevokedInIndex(ctx, taskID, namespace)
}

// IsRevoked inspects whether the token Secret (or TokenIndexSecretName when indexOnly is enabled) is marked revoked.
func (s *KubernetesSecretTokenStore) IsRevoked(ctx context.Context, taskID, namespace string) bool {
	if s.indexOnly {
		return s.isRevokedInIndex(ctx, taskID, namespace)
	}
	name := SecretName(taskID)
	sec := &corev1.Secret{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sec); err != nil {
		return s.isRevokedInIndex(ctx, taskID, namespace)
	}
	if !matchesTaskLabel(sec.Labels, taskID) {
		return false
	}
	if sec.Labels[LabelRevoked] == "true" {
		return true
	}
	return s.isRevokedInIndex(ctx, taskID, namespace)
}

// upsertTokenIndex records or updates a task's active token in the namespace TokenIndexSecretName Secret.
func (s *KubernetesSecretTokenStore) upsertTokenIndex(ctx context.Context, taskID, namespace string, token []byte) error {
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	return retry.RetryOnConflict(indexRetryBackoff, func() error {
		idx := &corev1.Secret{}
		err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: TokenIndexSecretName}, idx)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("get token index secret %s: %w", TokenIndexSecretName, err)
			}
			idx = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TokenIndexSecretName,
					Namespace: namespace,
					Labels: map[string]string{
						ManagedByLabel: "diverge",
					},
				},
				Data: map[string][]byte{
					taskID: append([]byte(nil), token...),
				},
			}
			if createErr := s.client.Create(ctx, idx); createErr != nil {
				if apierrors.IsAlreadyExists(createErr) {
					return apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, TokenIndexSecretName, createErr)
				}
				return createErr
			}
			return nil
		}
		if idx.Data == nil {
			idx.Data = make(map[string][]byte)
		}
		idx.Data[taskID] = append([]byte(nil), token...)
		if idx.Annotations != nil {
			delete(idx.Annotations, revokedIndexKey(taskID))
		}
		return s.client.Update(ctx, idx)
	})
}

// markRevokedInIndex marks a task's token as revoked in the namespace TokenIndexSecretName Secret.
func (s *KubernetesSecretTokenStore) markRevokedInIndex(ctx context.Context, taskID, namespace string) error {
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	return retry.RetryOnConflict(indexRetryBackoff, func() error {
		idx := &corev1.Secret{}
		err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: TokenIndexSecretName}, idx)
		if err != nil {
			if apierrors.IsNotFound(err) {
				if s.indexOnly {
					return ErrTokenNotFound
				}
				return nil
			}
			return fmt.Errorf("get token index secret %s: %w", TokenIndexSecretName, err)
		}
		delete(idx.Data, taskID)
		if idx.Annotations == nil {
			idx.Annotations = make(map[string]string)
		}
		idx.Annotations[revokedIndexKey(taskID)] = "true"
		return s.client.Update(ctx, idx)
	})
}

// removeFromTokenIndex removes a task's token and revocation state from the namespace TokenIndexSecretName Secret.
func (s *KubernetesSecretTokenStore) removeFromTokenIndex(ctx context.Context, taskID, namespace string) error {
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	return retry.RetryOnConflict(indexRetryBackoff, func() error {
		idx := &corev1.Secret{}
		err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: TokenIndexSecretName}, idx)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("get token index secret %s: %w", TokenIndexSecretName, err)
		}
		delete(idx.Data, taskID)
		if idx.Annotations != nil {
			delete(idx.Annotations, revokedIndexKey(taskID))
		}
		return s.client.Update(ctx, idx)
	})
}

// NoopTokenAuditor discards audit events.
type NoopTokenAuditor struct{}

var _ TokenAuditor = (*NoopTokenAuditor)(nil)

// RecordEvent discards the provided AuthEvent and returns nil.
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
