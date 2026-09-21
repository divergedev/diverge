package sandbox

import (
	"context"
	"io"
	"strings"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

// NoopSandboxProvider is a mock or testing implementation of SandboxProvider.
type NoopSandboxProvider struct{}

var _ pkgsandbox.SandboxProvider = (*NoopSandboxProvider)(nil)

// Provision returns a simulated ready sandbox.
func (p *NoopSandboxProvider) Provision(_ context.Context, task *v1alpha1.AgentTask) (*pkgsandbox.SandboxResult, error) {
	return &pkgsandbox.SandboxResult{
		ClaimName:      "noop-claim-" + task.Name,
		PodName:        "noop-pod-" + task.Name,
		PodIP:          "127.0.0.1",
		TokenMountPath: "/etc/diverge/token",
		Ready:          true,
		Message:        "Noop sandbox provider provisioned",
	}, nil
}

// Teardown does nothing.
func (p *NoopSandboxProvider) Teardown(_ context.Context, _ *v1alpha1.AgentTask) error {
	return nil
}

// Status returns a simulated ready status.
func (p *NoopSandboxProvider) Status(_ context.Context, task *v1alpha1.AgentTask) (*pkgsandbox.SandboxStatus, error) {
	return &pkgsandbox.SandboxStatus{
		ClaimName: "noop-claim-" + task.Name,
		PodName:   "noop-pod-" + task.Name,
		PodIP:     "127.0.0.1",
		Phase:     "Running",
		Ready:     true,
		Message:   "Noop sandbox provider running",
	}, nil
}

// StreamLogs returns a mock log stream.
func (p *NoopSandboxProvider) StreamLogs(_ context.Context, _ *v1alpha1.AgentTask, _ pkgsandbox.LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("Noop sandbox agent started\n")), nil
}
