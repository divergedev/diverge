//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/divergedev/diverge/api/v1alpha1"
)

var (
	defaultTimeout      = 3 * time.Minute
	controllerNamespace = "diverge-system"
)

func init() {
	if t := os.Getenv("E2E_TIMEOUT"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			defaultTimeout = d
		}
	}
	if ns := os.Getenv("DIVERGE_CONTROLLER_NAMESPACE"); ns != "" {
		controllerNamespace = ns
	}
}

// Framework holds the test context for E2E tests.
type Framework struct {
	Client     client.Client
	Clientset  *kubernetes.Clientset
	RestConfig *rest.Config
	Namespace  string
	T          *testing.T
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
	if err := gatewayv1.Install(c.Scheme()); err != nil {
		t.Fatalf("Failed to add gateway scheme: %v", err)
	}

	return &Framework{
		Client:     c,
		Clientset:  clientset,
		RestConfig: config,
		Namespace:  fmt.Sprintf("e2e-test-%d", time.Now().UnixNano()),
		T:          t,
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
	ctxDel, cancelDel := context.WithTimeout(ctx, 30*time.Second)
	defer cancelDel()
	if err := f.Clientset.CoreV1().Namespaces().Delete(ctxDel, f.Namespace, metav1.DeleteOptions{}); err != nil {
		f.T.Logf("Failed to delete namespace: %v", err)
	}

	// Best-effort wait for termination
	ctxWait, cancelWait := context.WithTimeout(ctx, 30*time.Second)
	defer cancelWait()
	for {
		var ns corev1.Namespace
		if err := f.Client.Get(ctxWait, types.NamespacedName{Name: f.Namespace}, &ns); err != nil {
			return // namespace gone
		}
		time.Sleep(500 * time.Millisecond)
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

// WaitForEnvironmentDeleted waits for an Environment to be deleted.
func (f *Framework) WaitForEnvironmentDeleted(ctx context.Context, name string, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timeout waiting for Environment %s to be deleted", name)
		default:
			var env v1alpha1.Environment
			err := f.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: f.Namespace}, &env)
			if apierrors.IsNotFound(err) {
				return nil
			}
			time.Sleep(1 * time.Second)
		}
	}
}

// CreateEnvironment creates an Environment CR and waits for it to be reconciled.
func (f *Framework) CreateEnvironment(ctx context.Context, env *v1alpha1.Environment) error {
	ctxCreate, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return f.Client.Create(ctxCreate, env)
}

// ControllerRunning checks if the diverge-controller deployment exists and has ready replicas.
func (f *Framework) ControllerRunning(ctx context.Context) bool {
	var dep appsv1.Deployment
	err := f.Client.Get(ctx, types.NamespacedName{
		Name: "diverge-controller", Namespace: controllerNamespace,
	}, &dep)
	return err == nil && dep.Status.ReadyReplicas > 0
}

// AnnotateNamespace adds annotations to the test namespace (e.g. for Linkerd injection).
func (f *Framework) AnnotateNamespace(ctx context.Context, namespace string, annotations map[string]string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ns, err := f.Clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		f.T.Fatalf("Failed to get namespace %s: %v", namespace, err)
	}
	if ns.Annotations == nil {
		ns.Annotations = map[string]string{}
	}
	for k, v := range annotations {
		ns.Annotations[k] = v
	}
	if _, err := f.Clientset.CoreV1().Namespaces().Update(ctx, ns, metav1.UpdateOptions{}); err != nil {
		f.T.Fatalf("Failed to annotate namespace %s: %v", namespace, err)
	}
}

// CreateNamespaceByName creates a namespace with a specific name (for cross-namespace tests).
func (f *Framework) CreateNamespaceByName(ctx context.Context, name string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	if _, err := f.Clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
		f.T.Fatalf("Failed to create namespace %s: %v", name, err)
	}
}

// CleanupNamespaceByName deletes a namespace by name.
func (f *Framework) CleanupNamespaceByName(ctx context.Context, name string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := f.Clientset.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		f.T.Logf("Failed to delete namespace %s: %v", name, err)
	}
}
