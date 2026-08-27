//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/kubernetes/scheme"
)

// DeployInClusterClient deploys a curl pod for testing GAMMA mesh routing.
func (f *Framework) DeployInClusterClient(ctx context.Context, name, namespace string) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": name,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "curl",
					Image:   "curlimages/curl:8.10.1",
					Command: []string{"sleep", "3600"},
				},
			},
		},
	}

	_, err := f.Clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create client pod: %w", err)
	}

	// Wait for pod to be ready
	err = wait.PollUntilContextTimeout(ctx, 2*time.Second, 1*time.Minute, true, func(ctx context.Context) (bool, error) {
		p, err := f.Clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		for _, cond := range p.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("client pod not ready: %w", err)
	}

	return nil
}

// SendMeshRequest sends an HTTP request from inside the client pod.
func (f *Framework) SendMeshRequest(ctx context.Context, clientPod, namespace, targetService string, port int32, headers map[string]string) (string, error) {
	curlCmd := []string{"curl", "-s"}
	for k, v := range headers {
		curlCmd = append(curlCmd, "-H", fmt.Sprintf("%s: %s", k, v))
	}
	curlCmd = append(curlCmd, fmt.Sprintf("http://%s:%d/", targetService, port))

	req := f.Clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(clientPod).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "curl",
			Command:   curlCmd,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(f.RestConfig, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	})
	if err != nil {
		return "", fmt.Errorf("curl execution failed: %w, stderr: %s", err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}
