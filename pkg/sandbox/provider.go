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

// ExecOptions specifies execution parameters inside an active sandbox container.
type ExecOptions struct {
	Command []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// SandboxExecutor is an optional interface implemented by providers that support
// running commands or diagnostic probes directly inside an active sandbox.
type SandboxExecutor interface {
	Exec(ctx context.Context, task *v1alpha1.AgentTask, opts ExecOptions) (exitCode int, err error)
}

// TerminalSize specifies terminal width and height for interactive sessions.
type TerminalSize struct {
	Width  uint16
	Height uint16
}

// AttachOptions specifies parameters for an interactive terminal attachment.
type AttachOptions struct {
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	TTY        bool
	ResizeChan <-chan TerminalSize
}

// SandboxAttacher is an optional interface for providers supporting interactive terminal attachments.
type SandboxAttacher interface {
	Attach(ctx context.Context, task *v1alpha1.AgentTask, opts AttachOptions) error
}

// PortForwardOptions specifies parameters for forwarding ports into the sandbox pod.
type PortForwardOptions struct {
	Ports     []string // e.g. ["8080:8080"]
	StopChan  <-chan struct{}
	ReadyChan chan struct{}
	Out       io.Writer
	ErrOut    io.Writer
}

// SandboxPortForwarder is an optional interface for forwarding network ports into an active sandbox.
type SandboxPortForwarder interface {
	PortForward(ctx context.Context, task *v1alpha1.AgentTask, opts PortForwardOptions) error
}

// SandboxFileTransfer is an optional interface for streaming files into or out of the sandbox workspace.
type SandboxFileTransfer interface {
	CopyTo(ctx context.Context, task *v1alpha1.AgentTask, src io.Reader, dstPath string) error
	CopyFrom(ctx context.Context, task *v1alpha1.AgentTask, srcPath string) (io.ReadCloser, error)
}

// PoolInfo reports the state of a pre-warmed sandbox pool.
type PoolInfo struct {
	Name       string
	Ready      int32
	Allocated  int32
	TargetSize int32
	Image      string
}

// SandboxPoolManager is an optional interface for managing pre-warmed sandbox worker pools.
type SandboxPoolManager interface {
	ListPools(ctx context.Context, namespace string) ([]PoolInfo, error)
	GetPool(ctx context.Context, namespace, name string) (*PoolInfo, error)
	ScalePool(ctx context.Context, namespace, name string, targetSize int32) error
}

// SandboxMetrics encapsulates runtime resource utilization for finops and observability.
type SandboxMetrics struct {
	CPUUsageMillicores int64
	MemoryUsageBytes   int64
	StorageUsageBytes  int64
	NetworkRxBytes     int64
	NetworkTxBytes     int64
}

// SandboxMetricsCollector is an optional interface for retrieving real-time resource utilization.
type SandboxMetricsCollector interface {
	GetMetrics(ctx context.Context, task *v1alpha1.AgentTask) (*SandboxMetrics, error)
}

// SnapshotOptions defines parameters for capturing a sandbox filesystem state.
type SnapshotOptions struct {
	Name        string
	Description string
}

// SandboxSnapshotter is an optional interface for capturing and restoring sandbox states.
type SandboxSnapshotter interface {
	Snapshot(ctx context.Context, task *v1alpha1.AgentTask, opts SnapshotOptions) (string, error)
	Restore(ctx context.Context, task *v1alpha1.AgentTask, snapshotID string) error
}

// Capability represents optional features supported by a SandboxProvider.
type Capability string

const (
	CapExec         Capability = "exec"
	CapAttach       Capability = "attach"
	CapPortForward  Capability = "port-forward"
	CapFileTransfer Capability = "file-transfer"
	CapPools        Capability = "pools"
	CapMetrics      Capability = "metrics"
	CapSnapshot     Capability = "snapshot"
)

// CapabilityDetector allows callers to discover supported features on a provider.
type CapabilityDetector interface {
	Supports(cap Capability) bool
}

// Providers is the global registry of available SandboxProvider implementations.
var Providers = registry.New[SandboxProvider]("sandbox")
