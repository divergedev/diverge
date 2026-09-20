package sandbox

import (
	"context"
	"io"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
)

// LogOptions specifies parameters for log streaming from the sandbox pod.
type LogOptions struct {
	Follow     bool
	TailLines  *int64
	Timestamps bool
}

// SandboxResult holds the immediate result of a Provision operation.
type SandboxResult struct {
	ClaimName      string
	PodName        string
	PodIP          string
	TokenMountPath string
	Ready          bool
	Message        string
}

// SandboxStatus holds the live observed status of an AgentTask's sandbox.
type SandboxStatus struct {
	ClaimName string
	PodName   string
	PodIP     string
	Phase     string
	Ready     bool
	Message   string
}

// SandboxProvider defines the contract for provisioning, observing, and tearing down
// isolated agent workspaces (e.g. K8s Agent Sandbox, gVisor pods).
type SandboxProvider interface {
	// Provision creates or claims an isolated sandbox workspace for the AgentTask.
	Provision(ctx context.Context, task *v1alpha1.AgentTask) (*SandboxResult, error)

	// Teardown cleanly releases the sandbox workspace and deletes associated resources.
	// Must be idempotent.
	Teardown(ctx context.Context, task *v1alpha1.AgentTask) error

	// Status returns the live runtime state of the sandbox.
	Status(ctx context.Context, task *v1alpha1.AgentTask) (*SandboxStatus, error)

	// StreamLogs opens a stream to the sandbox container logs.
	StreamLogs(ctx context.Context, task *v1alpha1.AgentTask, opts LogOptions) (io.ReadCloser, error)
}

// Providers is the global registry of available SandboxProvider implementations.
var Providers = registry.New[SandboxProvider]("sandbox")
