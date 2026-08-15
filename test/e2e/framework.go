//go:build e2e || e2e_dual

// Package e2e provides a test framework for end-to-end testing of the
// Diverge controller and environment lifecycle.
package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergev1 "github.com/divergedev/diverge/api/v1alpha1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Framework provides helpers for end-to-end tests, including Kubernetes
// client setup, environment creation, and condition polling.
type Framework struct {
	MgmtClient    client.Client
	MgmtClientset *kubernetes.Clientset
	ProdClient    client.Client
	ProdClientset *kubernetes.Clientset
}

// NewFramework creates a new end-to-end test Framework with default
// configuration. Call from TestMain or individual test setup.
func NewFramework(mgmtContext, prodContext string) (*Framework, error) {
	mgmtCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: mgmtContext},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load mgmt config: %w", err)
	}

	prodCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: prodContext},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load prod config: %w", err)
	}

	// Register schemes BEFORE creating clients — client.New copies the scheme
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = divergev1.AddToScheme(scheme)
	_ = gatewayv1.AddToScheme(scheme)

	// Bump rate limits for CI polling
	mgmtCfg.QPS = 50
	mgmtCfg.Burst = 100
	prodCfg.QPS = 50
	prodCfg.Burst = 100

	mgmtClient, err := client.New(mgmtCfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create mgmt client: %w", err)
	}

	prodClient, err := client.New(prodCfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create prod client: %w", err)
	}

	mgmtClientset, err := kubernetes.NewForConfig(mgmtCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create mgmt clientset: %w", err)
	}

	prodClientset, err := kubernetes.NewForConfig(prodCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create prod clientset: %w", err)
	}

	return &Framework{
		MgmtClient:    mgmtClient,
		MgmtClientset: mgmtClientset,
		ProdClient:    prodClient,
		ProdClientset: prodClientset,
	}, nil
}

// WaitForCondition polls until the condition function returns true, an error, or the timeout is reached.
func WaitForCondition(t *testing.T, timeout, interval time.Duration, conditionFn wait.ConditionWithContextFunc) {
	t.Helper()
	err := wait.PollUntilContextTimeout(context.Background(), interval, timeout, true, func(ctx context.Context) (bool, error) {
		return conditionFn(ctx)
	})
	require.NoError(t, err, "condition not met within timeout")
}

// WaitForPodReady waits for a pod matching the label selector to become ready in the given namespace.
func WaitForPodReady(t *testing.T, clientset *kubernetes.Clientset, namespace, labelSelector string, timeout time.Duration) {
	t.Helper()
	WaitForCondition(t, timeout, 2*time.Second, func(ctx context.Context) (bool, error) {
		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			return false, err
		}
		for _, pod := range pods.Items {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == "Ready" && cond.Status == "True" {
					return true, nil
				}
			}
		}
		return false, nil
	})
}

// WaitForResource waits for a resource to be created and readable via the controller-runtime client.
func WaitForResource(t *testing.T, c client.Client, key client.ObjectKey, obj client.Object, timeout time.Duration) {
	t.Helper()
	WaitForCondition(t, timeout, 2*time.Second, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, key, obj)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
}

// WaitForResourceGone waits for a resource to be deleted.
func WaitForResourceGone(t *testing.T, c client.Client, key client.ObjectKey, obj client.Object, timeout time.Duration) {
	t.Helper()
	WaitForCondition(t, timeout, 2*time.Second, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, key, obj)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
}

// SendHTTPRequest sends a GET request to the specified URL with the given headers and returns the status code and body.
func SendHTTPRequest(t *testing.T, url string, headers map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err, "failed to create http request")

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err, "failed to execute http request")
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "failed to read response body")

	return resp.StatusCode, string(bodyBytes)
}
