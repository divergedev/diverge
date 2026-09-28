//go:build !no_sandbox

package sandbox

import (
	"context"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

var (
	sandboxClaimGVK = schema.GroupVersionKind{
		Group:   "extensions.agents.x-k8s.io",
		Version: "v1beta1",
		Kind:    "SandboxClaim",
	}
)

// AgentSandboxProvider integrates with the Kubernetes Agent Sandbox (SIG Apps) controller.
type AgentSandboxProvider struct {
	client    client.Client
	clientset kubernetes.Interface
	scheme    *runtime.Scheme
}

var _ pkgsandbox.SandboxProvider = (*AgentSandboxProvider)(nil)

// NewAgentSandboxProvider initializes a new AgentSandboxProvider.
func NewAgentSandboxProvider(c client.Client, cs kubernetes.Interface, scheme *runtime.Scheme) *AgentSandboxProvider {
	return &AgentSandboxProvider{
		client:    c,
		clientset: cs,
		scheme:    scheme,
	}
}

// Provision creates a SandboxClaim for the AgentTask.
func (p *AgentSandboxProvider) Provision(ctx context.Context, task *v1alpha1.AgentTask) (*pkgsandbox.SandboxResult, error) {
	claimName := fmt.Sprintf("sb-claim-%s", task.Name)
	claim := &unstructured.Unstructured{}
	claim.SetGroupVersionKind(sandboxClaimGVK)
	claim.SetName(claimName)
	claim.SetNamespace(task.Namespace)

	claim.Object["spec"] = BuildSandboxClaimSpec(task)
	claim.SetLabels(map[string]string{
		"divergedev.com/agent-task":    task.Name,
		"app.kubernetes.io/managed-by": "diverge",
	})

	if p.scheme != nil {
		if err := controllerutil.SetControllerReference(task, claim, p.scheme); err != nil {
			return nil, fmt.Errorf("set controller reference: %w", err)
		}
	}

	err := p.client.Create(ctx, claim)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("create SandboxClaim: %w", err)
	}

	// Fetch status
	status, err := p.Status(ctx, task)
	if err != nil {
		return &pkgsandbox.SandboxResult{
			ClaimName: claimName,
			Ready:     false,
			Message:   "SandboxClaim created; waiting for binding",
		}, nil
	}

	return &pkgsandbox.SandboxResult{
		ClaimName:      claimName,
		PodName:        status.PodName,
		PodIP:          status.PodIP,
		TokenMountPath: "/etc/diverge/token",
		Ready:          status.Ready,
		Message:        status.Message,
	}, nil
}

// Teardown deletes the SandboxClaim.
func (p *AgentSandboxProvider) Teardown(ctx context.Context, task *v1alpha1.AgentTask) error {
	claimName := fmt.Sprintf("sb-claim-%s", task.Name)
	claim := &unstructured.Unstructured{}
	claim.SetGroupVersionKind(sandboxClaimGVK)
	claim.SetName(claimName)
	claim.SetNamespace(task.Namespace)

	if err := p.client.Delete(ctx, claim); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete SandboxClaim %s: %w", claimName, err)
	}
	return nil
}

// Status inspects the SandboxClaim and resolves its bound pod and phase.
func (p *AgentSandboxProvider) Status(ctx context.Context, task *v1alpha1.AgentTask) (*pkgsandbox.SandboxStatus, error) {
	claimName := fmt.Sprintf("sb-claim-%s", task.Name)
	claim := &unstructured.Unstructured{}
	claim.SetGroupVersionKind(sandboxClaimGVK)

	if err := p.client.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: claimName}, claim); err != nil {
		if apierrors.IsNotFound(err) {
			return &pkgsandbox.SandboxStatus{
				ClaimName: claimName,
				Phase:     "Pending",
				Ready:     false,
				Message:   "SandboxClaim not found",
			}, nil
		}
		return nil, fmt.Errorf("get SandboxClaim %s: %w", claimName, err)
	}

	statusMap, ok := claim.Object["status"].(map[string]interface{})
	if !ok || statusMap == nil {
		return &pkgsandbox.SandboxStatus{
			ClaimName: claimName,
			Phase:     "Pending",
			Ready:     false,
			Message:   "Waiting for sandbox assignment",
		}, nil
	}

	podName, _ := statusMap["podName"].(string)
	podIP, _ := statusMap["podIP"].(string)
	phase, _ := statusMap["phase"].(string)
	if phase == "" {
		phase = "Pending"
	}

	isReady := false
	if conditions, ok := statusMap["conditions"].([]interface{}); ok {
		for _, c := range conditions {
			if condMap, ok := c.(map[string]interface{}); ok {
				if condMap["type"] == "Ready" && condMap["status"] == "True" {
					isReady = true
					break
				}
			}
		}
	}

	return &pkgsandbox.SandboxStatus{
		ClaimName: claimName,
		PodName:   podName,
		PodIP:     podIP,
		Phase:     phase,
		Ready:     isReady,
		Message:   fmt.Sprintf("Sandbox phase: %s", phase),
	}, nil
}

// StreamLogs streams the logs of the sandbox pod.
func (p *AgentSandboxProvider) StreamLogs(ctx context.Context, task *v1alpha1.AgentTask, opts pkgsandbox.LogOptions) (io.ReadCloser, error) {
	status, err := p.Status(ctx, task)
	if err != nil {
		return nil, err
	}
	if status.PodName == "" {
		return nil, fmt.Errorf("sandbox pod not yet scheduled for task %s", task.Name)
	}

	if p.clientset == nil {
		return io.NopCloser(strings.NewReader("clientset not available for log streaming\n")), nil
	}

	podLogOpts := &corev1.PodLogOptions{
		Follow:     opts.Follow,
		Timestamps: opts.Timestamps,
	}
	if opts.TailLines != nil {
		podLogOpts.TailLines = opts.TailLines
	}

	streamCtx, cancel := context.WithCancel(ctx)
	req := p.clientset.CoreV1().Pods(task.Namespace).GetLogs(status.PodName, podLogOpts)
	stream, err := req.Stream(streamCtx)
	if err != nil {
		cancel()
		return nil, err
	}
	return &cancelableReadCloser{ReadCloser: stream, cancel: cancel}, nil
}

type cancelableReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelableReadCloser) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	return c.ReadCloser.Close()
}

// DefaultAgentImage is the fallback container image for autonomous agent sandboxes.
const DefaultAgentImage = "ghcr.io/divergedev/diverge-agent:latest"

// DefaultResourceBounds specifies conservative CPU and memory bounds for sandbox workloads.
var DefaultResourceBounds = map[string]interface{}{
	"requests": map[string]interface{}{
		"cpu":    "500m",
		"memory": "512Mi",
	},
	"limits": map[string]interface{}{
		"cpu":    "2",
		"memory": "2Gi",
	},
}

// BuildSandboxClaimSpec constructs a modular SandboxClaim spec according to task configurations.
func BuildSandboxClaimSpec(task *v1alpha1.AgentTask) map[string]interface{} {
	spec := map[string]interface{}{}
	if task.Spec.Sandbox.PoolRef != "" {
		spec["warmPoolRef"] = map[string]interface{}{
			"name": task.Spec.Sandbox.PoolRef,
		}
		return spec
	}
	if task.Spec.Sandbox.TemplateRef != "" {
		spec["templateRef"] = map[string]interface{}{
			"name": task.Spec.Sandbox.TemplateRef,
		}
		return spec
	}

	// Standalone pod template fallback with explicit resource bounds, ephemeral disk limits, and non-root security context
	spec["template"] = map[string]interface{}{
		"spec": map[string]interface{}{
			"securityContext": map[string]interface{}{
				"runAsNonRoot": true,
				"runAsUser":    int64(10001),
			},
			"containers": []interface{}{
				map[string]interface{}{
					"name":      "agent",
					"image":     DefaultAgentImage,
					"resources": DefaultResourceBounds,
					"securityContext": map[string]interface{}{
						"allowPrivilegeEscalation": false,
						"capabilities": map[string]interface{}{
							"drop": []interface{}{"ALL"},
						},
					},
					"volumeMounts": []interface{}{
						map[string]interface{}{
							"name":      "workspace",
							"mountPath": "/workspace",
						},
						map[string]interface{}{
							"name":      "token",
							"mountPath": "/etc/diverge/token",
							"readOnly":  true,
						},
					},
				},
			},
			"volumes": []interface{}{
				map[string]interface{}{
					"name": "workspace",
					"emptyDir": map[string]interface{}{
						"sizeLimit": "10Gi",
					},
				},
				map[string]interface{}{
					"name": "token",
					"secret": map[string]interface{}{
						"secretName":  fmt.Sprintf("%s-token", task.Name),
						"defaultMode": int64(0400),
					},
				},
			},
		},
	}
	return spec
}
