package cli

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var ErrServerNotFound = fmt.Errorf("diverge server not found in cluster")

func discoverServer(ctx context.Context, k8sClient kubernetes.Interface) (string, error) {
	listCtx, listCancel := context.WithTimeout(ctx, 10*time.Second)
	defer listCancel()
	svcs, err := k8sClient.CoreV1().Services("").List(listCtx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=diverge-server",
	})
	if err != nil {
		return "", fmt.Errorf("failed to list services: %w", err)
	}
	if len(svcs.Items) == 0 {
		return "", ErrServerNotFound
	}
	svc := svcs.Items[0]

	// Port-forward to the service for local tunnel access.
	cmd := exec.CommandContext(ctx, "kubectl", "port-forward",
		fmt.Sprintf("svc/%s", svc.Name),
		"-n", svc.Namespace,
		"18080:8080")

	err = cmd.Start()
	if err != nil {
		return "", err
	}

	// Wait a bit for it to be ready
	time.Sleep(1 * time.Second)

	return "http://localhost:18080", nil
}
