package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// Framework holds the test context for E2E tests.
type Framework struct {
	Client    client.Client
	Clientset *kubernetes.Clientset
	Namespace string
	T         *testing.T
}

// NewFramework creates a new E2E test framework.
func NewFramework(t *testing.T) *Framework {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		t.Fatalf("Failed to build config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("Failed to create clientset: %v", err)
	}

	c, err := client.New(config, client.Options{})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if err := v1alpha1.AddToScheme(c.Scheme()); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	return &Framework{
		Client:    c,
		Clientset: clientset,
		Namespace: fmt.Sprintf("e2e-test-%d", time.Now().UnixNano()),
		T:         t,
	}
}

// CreateNamespace creates a test namespace.
func (f *Framework) CreateNamespace(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: f.Namespace,
		},
	}
	if _, err := f.Clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
		f.T.Fatalf("Failed to create namespace: %v", err)
	}
}

// CleanupNamespace deletes the test namespace.
func (f *Framework) CleanupNamespace(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := f.Clientset.CoreV1().Namespaces().Delete(ctx, f.Namespace, metav1.DeleteOptions{}); err != nil {
		f.T.Logf("Failed to delete namespace: %v", err)
	}
}

// WaitForCondition waits for a condition on an Environment.
func (f *Framework) WaitForCondition(ctx context.Context, name, condType string, status metav1.ConditionStatus, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timeout waiting for condition %s=%s", condType, status)
		default:
			var env v1alpha1.Environment
			err := f.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: f.Namespace}, &env)
			if err == nil {
				for _, cond := range env.Status.Conditions {
					if cond.Type == condType && cond.Status == status {
						return nil
					}
				}
			}
			time.Sleep(1 * time.Second)
		}
	}
}

// CreateEnvironment creates an Environment CR and waits for it to be reconciled.
func (f *Framework) CreateEnvironment(ctx context.Context, env *v1alpha1.Environment) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return f.Client.Create(ctx, env)
}
