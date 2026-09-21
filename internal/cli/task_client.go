package cli

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TaskClient unifies all agent task operations behind a testable, decoupled interface.
type TaskClient interface {
	TaskGuider
	Create(ctx context.Context, task *v1alpha1.AgentTask) error
	Get(ctx context.Context, namespace, name string) (*v1alpha1.AgentTask, error)
	List(ctx context.Context, namespace string) ([]v1alpha1.AgentTask, error)
	Delete(ctx context.Context, namespace, name string) error
	Pause(ctx context.Context, namespace, name string) error
	Resume(ctx context.Context, namespace, name string, budgetUSD string, tokens int64) error
}

// KubeTaskClient implements TaskClient using direct Kubernetes API calls.
type KubeTaskClient struct {
	client client.Client
}

var _ TaskClient = (*KubeTaskClient)(nil)

// NewKubeTaskClient constructs a KubeTaskClient.
func NewKubeTaskClient(c client.Client) *KubeTaskClient {
	return &KubeTaskClient{client: c}
}

// Create persists a new AgentTask.
func (c *KubeTaskClient) Create(ctx context.Context, task *v1alpha1.AgentTask) error {
	if err := c.client.Create(ctx, task); err != nil {
		return fmt.Errorf("create AgentTask %s: %w", task.Name, err)
	}
	return nil
}

// Get retrieves an AgentTask by namespace and name.
func (c *KubeTaskClient) Get(ctx context.Context, namespace, name string) (*v1alpha1.AgentTask, error) {
	task := &v1alpha1.AgentTask{}
	if err := c.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, task); err != nil {
		return nil, fmt.Errorf("get AgentTask %s: %w", name, err)
	}
	return task, nil
}

// List returns all AgentTasks in a namespace.
func (c *KubeTaskClient) List(ctx context.Context, namespace string) ([]v1alpha1.AgentTask, error) {
	taskList := &v1alpha1.AgentTaskList{}
	if err := c.client.List(ctx, taskList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list AgentTasks in namespace %s: %w", namespace, err)
	}
	return taskList.Items, nil
}

// Delete removes an AgentTask.
func (c *KubeTaskClient) Delete(ctx context.Context, namespace, name string) error {
	task := &v1alpha1.AgentTask{}
	if err := c.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, task); err != nil {
		return fmt.Errorf("get AgentTask %s: %w", name, err)
	}
	if err := c.client.Delete(ctx, task); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete AgentTask %s: %w", name, err)
	}
	return nil
}

// Pause suspends execution of an AgentTask.
func (c *KubeTaskClient) Pause(ctx context.Context, namespace, name string) error {
	task, err := c.Get(ctx, namespace, name)
	if err != nil {
		return err
	}
	task.Spec.Suspended = true
	if err := c.client.Update(ctx, task); err != nil {
		return fmt.Errorf("pause AgentTask %s: %w", name, err)
	}
	return nil
}

// Resume unpauses execution of an AgentTask and optionally bumps budget or tokens.
func (c *KubeTaskClient) Resume(ctx context.Context, namespace, name string, budgetUSD string, tokens int64) error {
	task, err := c.Get(ctx, namespace, name)
	if err != nil {
		return err
	}
	task.Spec.Suspended = false
	if budgetUSD != "" {
		task.Spec.BudgetUSD = budgetUSD
	}
	if tokens > 0 {
		task.Spec.BudgetTokens = tokens
	}
	if err := c.client.Update(ctx, task); err != nil {
		return fmt.Errorf("resume AgentTask %s: %w", name, err)
	}
	return nil
}

// Guide sets steering annotations on an active AgentTask.
func (c *KubeTaskClient) Guide(ctx context.Context, namespace, name, message string) error {
	task, err := c.Get(ctx, namespace, name)
	if err != nil {
		return err
	}
	if task.Annotations == nil {
		task.Annotations = make(map[string]string)
	}
	task.Annotations[AnnotationGuidance] = message
	task.Annotations[AnnotationGuidanceTimestamp] = time.Now().UTC().Format(time.RFC3339)
	if err := c.client.Update(ctx, task); err != nil {
		return fmt.Errorf("guide AgentTask %s: %w", name, err)
	}
	return nil
}

// MockTaskClient provides an in-memory implementation of TaskClient for testing.
type MockTaskClient struct {
	mu    sync.RWMutex
	tasks map[string]*v1alpha1.AgentTask
}

var _ TaskClient = (*MockTaskClient)(nil)

// NewMockTaskClient constructs an in-memory MockTaskClient.
func NewMockTaskClient() *MockTaskClient {
	return &MockTaskClient{
		tasks: make(map[string]*v1alpha1.AgentTask),
	}
}

func taskKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

// Create stores the task in memory.
func (m *MockTaskClient) Create(_ context.Context, task *v1alpha1.AgentTask) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := taskKey(task.Namespace, task.Name)
	if _, exists := m.tasks[key]; exists {
		return fmt.Errorf("agent task %q already exists", task.Name)
	}
	m.tasks[key] = task.DeepCopy()
	return nil
}

// Get retrieves a task from memory.
func (m *MockTaskClient) Get(_ context.Context, namespace, name string) (*v1alpha1.AgentTask, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := taskKey(namespace, name)
	task, ok := m.tasks[key]
	if !ok {
		return nil, fmt.Errorf("agent task %q not found", name)
	}
	return task.DeepCopy(), nil
}

// List returns all tasks in memory for the given namespace.
func (m *MockTaskClient) List(_ context.Context, namespace string) ([]v1alpha1.AgentTask, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var res []v1alpha1.AgentTask
	for _, t := range m.tasks {
		if namespace == "" || t.Namespace == namespace {
			res = append(res, *t.DeepCopy())
		}
	}
	return res, nil
}

// Delete removes a task from memory.
func (m *MockTaskClient) Delete(_ context.Context, namespace, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tasks, taskKey(namespace, name))
	return nil
}

// Pause marks the task suspended.
func (m *MockTaskClient) Pause(_ context.Context, namespace, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := taskKey(namespace, name)
	task, ok := m.tasks[key]
	if !ok {
		return fmt.Errorf("agent task %q not found", name)
	}
	task.Spec.Suspended = true
	return nil
}

// Resume removes suspension and updates budgets.
func (m *MockTaskClient) Resume(_ context.Context, namespace, name string, budgetUSD string, tokens int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := taskKey(namespace, name)
	task, ok := m.tasks[key]
	if !ok {
		return fmt.Errorf("agent task %q not found", name)
	}
	task.Spec.Suspended = false
	if budgetUSD != "" {
		task.Spec.BudgetUSD = budgetUSD
	}
	if tokens > 0 {
		task.Spec.BudgetTokens = tokens
	}
	return nil
}

// Guide sets guidance annotations on the in-memory task.
func (m *MockTaskClient) Guide(_ context.Context, namespace, name, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := taskKey(namespace, name)
	task, ok := m.tasks[key]
	if !ok {
		return fmt.Errorf("agent task %q not found", name)
	}
	if task.Annotations == nil {
		task.Annotations = make(map[string]string)
	}
	task.Annotations[AnnotationGuidance] = message
	task.Annotations[AnnotationGuidanceTimestamp] = time.Now().UTC().Format(time.RFC3339)
	return nil
}
