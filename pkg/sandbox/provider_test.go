package sandbox_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

type fullMockProvider struct {
	caps map[pkgsandbox.Capability]bool
}

var (
	_ pkgsandbox.SandboxProvider         = (*fullMockProvider)(nil)
	_ pkgsandbox.SandboxExecutor         = (*fullMockProvider)(nil)
	_ pkgsandbox.SandboxAttacher         = (*fullMockProvider)(nil)
	_ pkgsandbox.SandboxPortForwarder    = (*fullMockProvider)(nil)
	_ pkgsandbox.SandboxFileTransfer     = (*fullMockProvider)(nil)
	_ pkgsandbox.SandboxPoolManager      = (*fullMockProvider)(nil)
	_ pkgsandbox.SandboxMetricsCollector = (*fullMockProvider)(nil)
	_ pkgsandbox.SandboxSnapshotter      = (*fullMockProvider)(nil)
	_ pkgsandbox.CapabilityDetector      = (*fullMockProvider)(nil)
)

func newFullMockProvider() *fullMockProvider {
	return &fullMockProvider{
		caps: map[pkgsandbox.Capability]bool{
			pkgsandbox.CapExec:         true,
			pkgsandbox.CapAttach:       true,
			pkgsandbox.CapPortForward:  true,
			pkgsandbox.CapFileTransfer: true,
			pkgsandbox.CapPools:        true,
			pkgsandbox.CapMetrics:      true,
			pkgsandbox.CapSnapshot:     true,
		},
	}
}

func (m *fullMockProvider) Provision(_ context.Context, task *v1alpha1.AgentTask) (*pkgsandbox.SandboxResult, error) {
	return &pkgsandbox.SandboxResult{
		ClaimName: "claim-" + task.Name,
		PodName:   "pod-" + task.Name,
		Ready:     true,
	}, nil
}

func (m *fullMockProvider) Teardown(_ context.Context, _ *v1alpha1.AgentTask) error {
	return nil
}

func (m *fullMockProvider) Status(_ context.Context, task *v1alpha1.AgentTask) (*pkgsandbox.SandboxStatus, error) {
	return &pkgsandbox.SandboxStatus{
		ClaimName: "claim-" + task.Name,
		Ready:     true,
		Phase:     "Running",
	}, nil
}

func (m *fullMockProvider) StreamLogs(_ context.Context, _ *v1alpha1.AgentTask, _ pkgsandbox.LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("log stream")), nil
}

func (m *fullMockProvider) Exec(_ context.Context, _ *v1alpha1.AgentTask, opts pkgsandbox.ExecOptions) (int, error) {
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte("exec output\n"))
	}
	return 0, nil
}

func (m *fullMockProvider) Attach(_ context.Context, _ *v1alpha1.AgentTask, opts pkgsandbox.AttachOptions) error {
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte("attached\n"))
	}
	return nil
}

func (m *fullMockProvider) PortForward(_ context.Context, _ *v1alpha1.AgentTask, opts pkgsandbox.PortForwardOptions) error {
	if opts.ReadyChan != nil {
		close(opts.ReadyChan)
	}
	return nil
}

func (m *fullMockProvider) CopyTo(_ context.Context, _ *v1alpha1.AgentTask, src io.Reader, _ string) error {
	_, err := io.ReadAll(src)
	return err
}

func (m *fullMockProvider) CopyFrom(_ context.Context, _ *v1alpha1.AgentTask, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("file content")), nil
}

func (m *fullMockProvider) ListPools(_ context.Context, _ string) ([]pkgsandbox.PoolInfo, error) {
	return []pkgsandbox.PoolInfo{
		{Name: "default-pool", Ready: 5, Allocated: 2, TargetSize: 10, Image: "diverge-agent:latest"},
	}, nil
}

func (m *fullMockProvider) GetPool(_ context.Context, _, name string) (*pkgsandbox.PoolInfo, error) {
	return &pkgsandbox.PoolInfo{Name: name, Ready: 5, Allocated: 2, TargetSize: 10}, nil
}

func (m *fullMockProvider) ScalePool(_ context.Context, _, _ string, _ int32) error {
	return nil
}

func (m *fullMockProvider) GetMetrics(_ context.Context, _ *v1alpha1.AgentTask) (*pkgsandbox.SandboxMetrics, error) {
	return &pkgsandbox.SandboxMetrics{
		CPUUsageMillicores: 250,
		MemoryUsageBytes:   256 * 1024 * 1024,
	}, nil
}

func (m *fullMockProvider) Snapshot(_ context.Context, _ *v1alpha1.AgentTask, opts pkgsandbox.SnapshotOptions) (string, error) {
	return "snap-" + opts.Name, nil
}

func (m *fullMockProvider) Restore(_ context.Context, _ *v1alpha1.AgentTask, _ string) error {
	return nil
}

func (m *fullMockProvider) Supports(cap pkgsandbox.Capability) bool {
	return m.caps[cap]
}

func TestSandboxProviderCapabilities(t *testing.T) {
	ctx := context.Background()
	task := &v1alpha1.AgentTask{
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Build feature",
		},
	}
	task.Name = "test-task"

	provider := newFullMockProvider()

	// 1. Core Provider Contract
	res, err := provider.Provision(ctx, task)
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	if !res.Ready || res.ClaimName != "claim-test-task" {
		t.Fatalf("unexpected provision result: %+v", res)
	}

	status, err := provider.Status(ctx, task)
	if err != nil || !status.Ready {
		t.Fatalf("Status failed: %v, status: %+v", err, status)
	}

	logs, err := provider.StreamLogs(ctx, task, pkgsandbox.LogOptions{})
	if err != nil {
		t.Fatalf("StreamLogs failed: %v", err)
	}
	logBytes, _ := io.ReadAll(logs)
	_ = logs.Close()
	if string(logBytes) != "log stream" {
		t.Errorf("unexpected logs: %q", string(logBytes))
	}

	// 2. Capability Detection
	var detector pkgsandbox.CapabilityDetector = provider
	caps := []pkgsandbox.Capability{
		pkgsandbox.CapExec,
		pkgsandbox.CapAttach,
		pkgsandbox.CapPortForward,
		pkgsandbox.CapFileTransfer,
		pkgsandbox.CapPools,
		pkgsandbox.CapMetrics,
		pkgsandbox.CapSnapshot,
	}
	for _, c := range caps {
		if !detector.Supports(c) {
			t.Errorf("expected support for capability %s", c)
		}
	}

	// 3. Optional Interfaces
	var exec pkgsandbox.SandboxExecutor = provider
	var stdout bytes.Buffer
	code, err := exec.Exec(ctx, task, pkgsandbox.ExecOptions{Stdout: &stdout})
	if err != nil || code != 0 || stdout.String() != "exec output\n" {
		t.Errorf("Exec error: code=%d, err=%v, stdout=%q", code, err, stdout.String())
	}

	var attacher pkgsandbox.SandboxAttacher = provider
	var attachOut bytes.Buffer
	if err := attacher.Attach(ctx, task, pkgsandbox.AttachOptions{Stdout: &attachOut}); err != nil {
		t.Errorf("Attach error: %v", err)
	}
	if attachOut.String() != "attached\n" {
		t.Errorf("Attach unexpected output: %q", attachOut.String())
	}

	var pf pkgsandbox.SandboxPortForwarder = provider
	readyCh := make(chan struct{})
	if err := pf.PortForward(ctx, task, pkgsandbox.PortForwardOptions{ReadyChan: readyCh}); err != nil {
		t.Errorf("PortForward error: %v", err)
	}
	<-readyCh

	var ft pkgsandbox.SandboxFileTransfer = provider
	if err := ft.CopyTo(ctx, task, strings.NewReader("sample"), "/tmp/sample"); err != nil {
		t.Errorf("CopyTo error: %v", err)
	}
	reader, err := ft.CopyFrom(ctx, task, "/tmp/sample")
	if err != nil {
		t.Errorf("CopyFrom error: %v", err)
	} else {
		data, _ := io.ReadAll(reader)
		_ = reader.Close()
		if string(data) != "file content" {
			t.Errorf("CopyFrom data mismatch: %q", string(data))
		}
	}

	var poolMgr pkgsandbox.SandboxPoolManager = provider
	pools, err := poolMgr.ListPools(ctx, "default")
	if err != nil || len(pools) != 1 || pools[0].Name != "default-pool" {
		t.Errorf("ListPools error: %v, pools: %+v", err, pools)
	}

	var metricsCol pkgsandbox.SandboxMetricsCollector = provider
	metrics, err := metricsCol.GetMetrics(ctx, task)
	if err != nil || metrics.CPUUsageMillicores != 250 {
		t.Errorf("GetMetrics error: %v, metrics: %+v", err, metrics)
	}

	var snapshotter pkgsandbox.SandboxSnapshotter = provider
	snapID, err := snapshotter.Snapshot(ctx, task, pkgsandbox.SnapshotOptions{Name: "pre-test"})
	if err != nil || snapID != "snap-pre-test" {
		t.Errorf("Snapshot error: %v, snapID=%s", err, snapID)
	}
	if err := snapshotter.Restore(ctx, task, snapID); err != nil {
		t.Errorf("Restore error: %v", err)
	}

	if err := provider.Teardown(ctx, task); err != nil {
		t.Errorf("Teardown error: %v", err)
	}
}
