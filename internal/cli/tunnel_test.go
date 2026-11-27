package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/internal/tunnelclient"
)

type stubStandaloneTunnelServer struct {
	divergev1alpha1connect.UnimplementedTunnelServiceHandler
	mu       sync.Mutex
	gotRegs  []*pb.TunnelRegister
	gotAuths []string
	tunnels  []*pb.TunnelInfo
	listErr  error
	holdOpen bool
}

func (s *stubStandaloneTunnelServer) Tunnel(ctx context.Context, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	msg, err := stream.Receive()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.gotRegs = append(s.gotRegs, msg.GetRegister())
	s.gotAuths = append(s.gotAuths, stream.RequestHeader().Get("Authorization"))
	s.mu.Unlock()

	if err := stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_Ready{
			Ready: &pb.TunnelReady{
				TunnelId:        "tun-standalone-1",
				Endpoint:        "diverge-server-tunnel-proxy:8081",
				ProxyService:    "diverge-server-tunnel-proxy:8081",
				K8SServiceName:  "diverge-tun-abc",
				ProtocolVersion: 3,
			},
		},
	}); err != nil {
		return err
	}

	if s.holdOpen {
		<-ctx.Done()
	}
	return nil
}

func (s *stubStandaloneTunnelServer) ListTunnels(_ context.Context, _ *connect.Request[pb.ListTunnelsRequest]) (*connect.Response[pb.ListTunnelsResponse], error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return connect.NewResponse(&pb.ListTunnelsResponse{Tunnels: s.tunnels}), nil
}

func TestLoadUserConfigFrom(t *testing.T) {
	t.Run("missing file returns empty config", func(t *testing.T) {
		cfg, err := LoadUserConfigFrom(filepath.Join(t.TempDir(), "nonexistent.yaml"))
		require.NoError(t, err)
		assert.Empty(t, cfg.Server)
		assert.Empty(t, cfg.DefaultNamespace)
	})

	t.Run("valid v1 config loads server and default_namespace", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(cfgPath, []byte("version: 1\nserver: https://diverge.example.com\ndefault_namespace: dev\n"), 0o600))
		cfg, err := LoadUserConfigFrom(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, 1, cfg.Version)
		assert.Equal(t, "https://diverge.example.com", cfg.Server)
		assert.Equal(t, "dev", cfg.DefaultNamespace)
	})

	t.Run("unsupported version rejected", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(cfgPath, []byte("version: 99\nserver: https://diverge.example.com\n"), 0o600))
		_, err := LoadUserConfigFrom(cfgPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported user config version")
	})
}

func TestLoadTunnelDefaults_DualFormatsAndUntrustedRepoIsolation(t *testing.T) {
	// editorconfig-checker-disable
	t.Run("ServicePreview format loads safe defaults and ignores malicious keys", func(t *testing.T) {
		dir := t.TempDir()
		content := `apiVersion: divergedev.com/v1alpha1
kind: ServicePreview
metadata:
  name: web
token_cmd: "rm -rf /"
token: "evil-tok"
token_file: "/etc/passwd"
server: "https://evil.example.com"
local_host: "169.254.169.254"
spec:
  serviceName: orders-api
  namespace: staging
  port: 3001
  token_cmd: "id"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".diverge.yaml"), []byte(content), 0o600))
		defaults, err := LoadTunnelDefaults(dir)
		require.NoError(t, err)
		assert.Equal(t, TunnelDefaults{Service: "orders-api", Namespace: "staging", Port: 3001}, defaults)
	})

	t.Run("version 1 format with single service loads service and baseline_namespace while ignoring malicious keys", func(t *testing.T) {
		dir := t.TempDir()
		content := `version: "1"
token_cmd: "echo evil"
token_file: "/etc/shadow"
server: "https://evil.example.com"
local_host: "169.254.169.254"
services:
  billing:
    paths: ["services/billing/**"]
defaults:
  routing:
    baseline_namespace: dev
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".diverge.yaml"), []byte(content), 0o600))
		defaults, err := LoadTunnelDefaults(dir)
		require.NoError(t, err)
		assert.Equal(t, TunnelDefaults{Service: "billing", Namespace: "dev", Port: 0}, defaults)
	})

	t.Run("version 1 format with multiple services does not guess service", func(t *testing.T) {
		dir := t.TempDir()
		content := `version: "1"
services:
  web: {}
  api: {}
defaults:
  routing:
    baseline_namespace: dev
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".diverge.yaml"), []byte(content), 0o600))
		defaults, err := LoadTunnelDefaults(dir)
		require.NoError(t, err)
		assert.Equal(t, TunnelDefaults{Service: "", Namespace: "dev", Port: 0}, defaults)
	})
	// editorconfig-checker-enable
}

func TestTunnelCmd_ZeroKubeconfigJSONAndChildExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test helper relies on sh")
	}

	t.Setenv("KUBECONFIG", "/nonexistent/path/kubeconfig-does-not-exist")
	t.Setenv(tunnelTokenEnvVar, "standalone-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	workDir := t.TempDir()
	// editorconfig-checker-disable
	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".diverge.yaml"), []byte(`version: "1"
server: "https://malicious.example.com"
token_cmd: "false"
services:
  checkout: {}
defaults:
  routing:
    baseline_namespace: dev
`), 0o600))
	// editorconfig-checker-enable

	srv := &stubStandaloneTunnelServer{holdOpen: true}
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(srv)
	mux.Handle(path, handler)
	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	app := &App{}
	root := NewRootCmd(app)
	// Swap the registered tunnel command with one wired with test HTTP client and workDir.
	for _, sub := range root.Commands() {
		if sub.Name() == "tunnel" {
			root.RemoveCommand(sub)
			break
		}
	}
	root.AddCommand(newTunnelCmdWithDeps(app, &tunnelDeps{
		detector:   fakeDetector{username: "alice", gitBranch: "feat/pay"},
		workDir:    workDir,
		httpClient: ts.Client(),
	}))

	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{
		"tunnel",
		"--server", ts.URL,
		"--preview-id", "pr-9",
		"--port", "4000",
		"--meta", "kind=module",
		"--force",
		"--skip-k8s-service",
		"--json",
		"--",
		"sh", "-c", "echo noisy-child-stdout; echo noisy-child-stderr >&2; sleep 0.15; exit 7",
	})

	err := root.ExecuteContext(t.Context())
	require.Error(t, err)
	var childExit *ChildExitError
	require.True(t, errors.As(err, &childExit), "expected ChildExitError, got %v", err)
	assert.Equal(t, 7, childExit.Code)

	// Verify stdout contains ONLY valid NDJSON lines and NO noisy child output.
	stdoutStr := stdout.String()
	assert.NotContains(t, stdoutStr, "noisy-child-stdout")
	assert.NotContains(t, stdoutStr, "noisy-child-stderr")
	assert.NotContains(t, stdoutStr, "[diverge]")

	lines := strings.Split(strings.TrimSpace(stdoutStr), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	events := map[string]tunnelJSONEvent{}
	for _, line := range lines {
		var ev tunnelJSONEvent
		require.NoError(t, json.Unmarshal([]byte(line), &ev), "every stdout line under --json must be valid JSON: %s", line)
		events[ev.Event] = ev
	}
	require.Contains(t, events, "ready")
	assert.Equal(t, "tun-standalone-1", events["ready"].TunnelID)
	assert.Equal(t, "dev", events["ready"].Namespace)
	assert.Equal(t, "checkout", events["ready"].Service)
	assert.Equal(t, int32(3), events["ready"].ProtocolVersion)

	require.Contains(t, events, "child_exited")
	require.NotNil(t, events["child_exited"].ExitCode)
	assert.Equal(t, 7, *events["child_exited"].ExitCode)

	// Child noisy output went safely to stderr.
	assert.Contains(t, stderr.String(), "noisy-child-stdout")
	assert.Contains(t, stderr.String(), "noisy-child-stderr")

	srv.mu.Lock()
	require.Len(t, srv.gotRegs, 1)
	assert.Equal(t, "checkout", srv.gotRegs[0].Service)
	assert.Equal(t, "dev", srv.gotRegs[0].Namespace)
	assert.Equal(t, int32(4000), srv.gotRegs[0].Port)
	assert.Equal(t, "pr-9", srv.gotRegs[0].PreviewId)
	assert.Equal(t, "module", srv.gotRegs[0].Metadata["kind"])
	assert.True(t, srv.gotRegs[0].Force)
	assert.True(t, srv.gotRegs[0].SkipK8SService)
	assert.NotEmpty(t, srv.gotRegs[0].ClientInstanceId)
	assert.Equal(t, "Bearer standalone-token", srv.gotAuths[0])
	srv.mu.Unlock()
}

func TestTunnelCmd_WaitReadyAndRejections(t *testing.T) {
	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	t.Setenv(tunnelTokenEnvVar, "tok")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	t.Run("rejects non-loopback --local-host", func(t *testing.T) {
		app := &App{}
		cmd := newTunnelCmdWithDeps(app, &tunnelDeps{workDir: t.TempDir()})
		cmd.SetArgs([]string{"--server", "https://diverge.example.com", "--service", "web", "--local-host", "169.254.169.254"})
		err := cmd.ExecuteContext(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "loopback")
	})

	t.Run("rejects invalid --meta", func(t *testing.T) {
		app := &App{}
		cmd := newTunnelCmdWithDeps(app, &tunnelDeps{workDir: t.TempDir()})
		cmd.SetArgs([]string{"--server", "https://diverge.example.com", "--service", "web", "--meta", "missing-equals"})
		err := cmd.ExecuteContext(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "key=value")
	})

	t.Run("wait-ready blocks registration until local port listens", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := ln.Addr().String()
		_, portStr, err := net.SplitHostPort(addr)
		require.NoError(t, err)
		require.NoError(t, ln.Close())

		srv := &stubStandaloneTunnelServer{holdOpen: true}
		mux := http.NewServeMux()
		path, handler := divergev1alpha1connect.NewTunnelServiceHandler(srv)
		mux.Handle(path, handler)
		ts := httptest.NewUnstartedServer(mux)
		ts.EnableHTTP2 = true
		ts.StartTLS()
		defer ts.Close()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go func() {
			time.Sleep(150 * time.Millisecond)
			delayedLn, lErr := net.Listen("tcp", addr)
			if lErr != nil {
				return
			}
			defer func() { _ = delayedLn.Close() }()
			<-ctx.Done()
		}()

		var out, errOut syncBuffer
		cmd := newTunnelCmdWithDeps(&App{}, &tunnelDeps{
			workDir:    t.TempDir(),
			httpClient: ts.Client(),
		})
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs([]string{
			"--server", ts.URL,
			"--service", "web",
			"--preview-id", "wait-test",
			"--port", portStr,
			"--wait-ready",
			"--wait-ready-timeout", "3s",
		})

		done := make(chan error, 1)
		go func() { done <- cmd.ExecuteContext(ctx) }()

		require.Eventually(t, func() bool {
			srv.mu.Lock()
			regs := len(srv.gotRegs)
			srv.mu.Unlock()
			return regs == 1 && strings.Contains(out.String(), "Tunnel active")
		}, 4*time.Second, 25*time.Millisecond)

		cancel()
		require.NoError(t, <-done)
	})
}

func TestTunnelListCmd(t *testing.T) {
	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	t.Setenv(tunnelTokenEnvVar, "list-tok")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	srv := &stubStandaloneTunnelServer{
		tunnels: []*pb.TunnelInfo{
			{
				TunnelId:  "tun-1",
				Namespace: "dev",
				PreviewId: "feat-x",
				Service:   "web",
				Port:      3000,
				Owner:     "alice",
				ExpiresAt: timestamppb.New(time.Unix(1700000000, 0)),
			},
		},
	}
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewTunnelServiceHandler(srv)
	mux.Handle(path, handler)
	ts := httptest.NewUnstartedServer(mux)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	runList := func(extraArgs ...string) string {
		var stdout bytes.Buffer
		app := &App{}
		root := NewRootCmd(app)
		for _, sub := range root.Commands() {
			if sub.Name() == "tunnel" {
				root.RemoveCommand(sub)
				break
			}
		}
		root.AddCommand(newTunnelCmdWithDeps(app, &tunnelDeps{httpClient: ts.Client()}))
		root.SetOut(&stdout)
		root.SetArgs(append([]string{"tunnel", "list", "--server", ts.URL}, extraArgs...))
		require.NoError(t, root.ExecuteContext(t.Context()))
		return stdout.String()
	}

	tableOut := runList("-n", "dev")
	assert.Contains(t, tableOut, "TUNNEL ID")
	assert.Contains(t, tableOut, "tun-1")
	assert.Contains(t, tableOut, "alice")

	jsonOut := runList("--json")
	var items []tunnelListItem
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &items))
	require.Len(t, items, 1)
	assert.Equal(t, "tun-1", items[0].TunnelID)
	assert.Equal(t, int32(3000), items[0].Port)

	// Empty list with --json outputs [].
	srv.tunnels = nil
	emptyJSON := runList("--json")
	assert.Equal(t, "[]\n", emptyJSON)
	_ = strconv.Itoa(0)
}

func TestLoadTunnelDefaults_OversizedRejected(t *testing.T) {
	dir := t.TempDir()
	oversized := bytes.Repeat([]byte("a"), (5<<20)+1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".diverge.yaml"), oversized, 0o600))

	_, err := LoadTunnelDefaults(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestResolveServerAddr_InsecureHTTPRejected(t *testing.T) {
	t.Setenv("DIVERGE_SERVER", "")

	_, err := resolveServerAddr("http://diverge.example.com:8081", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, tunnelclient.ErrInsecureHTTP)

	gotLocalIP, err := resolveServerAddr("http://127.0.0.1:8081/", nil)
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:8081", gotLocalIP)

	gotLocalhost, err := resolveServerAddr("http://localhost:8081", nil)
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8081", gotLocalhost)

	gotHTTPS, err := resolveServerAddr("https://diverge.example.com/", nil)
	require.NoError(t, err)
	assert.Equal(t, "https://diverge.example.com", gotHTTPS)
}

func TestTunnelCmd_HardeningAndJSONErrors(t *testing.T) {
	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	t.Setenv(tunnelTokenEnvVar, "tok")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	t.Run("rejects positional args before double-dash", func(t *testing.T) {
		cmd := newTunnelCmdWithDeps(&App{}, &tunnelDeps{workDir: t.TempDir()})
		cmd.SetArgs([]string{"extra", "--", "sh", "-c", "true"})
		err := cmd.ExecuteContext(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected arguments")
	})

	t.Run("rejects port suffix in --local-host", func(t *testing.T) {
		cmd := newTunnelCmdWithDeps(&App{}, &tunnelDeps{workDir: t.TempDir()})
		cmd.SetArgs([]string{"--server", "https://diverge.example.com", "--service", "web", "--local-host", "127.0.0.1:9999"})
		err := cmd.ExecuteContext(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "loopback")
	})

	t.Run("rejects non-positive --wait-ready-timeout under --json", func(t *testing.T) {
		var out, errOut bytes.Buffer
		cmd := newTunnelCmdWithDeps(&App{}, &tunnelDeps{workDir: t.TempDir()})
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs([]string{
			"--server", "https://diverge.example.com",
			"--service", "web",
			"--wait-ready",
			"--wait-ready-timeout", "0s",
			"--json",
		})
		err := cmd.ExecuteContext(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "greater than 0")

		var ev tunnelJSONEvent
		require.NoError(t, json.Unmarshal(bytes.TrimSpace(out.Bytes()), &ev))
		assert.Equal(t, "error", ev.Event)
		assert.Contains(t, ev.Error, "greater than 0")
	})

	t.Run("child start failure emits NDJSON error under --json", func(t *testing.T) {
		srv := &stubStandaloneTunnelServer{holdOpen: true}
		mux := http.NewServeMux()
		path, handler := divergev1alpha1connect.NewTunnelServiceHandler(srv)
		mux.Handle(path, handler)
		ts := httptest.NewUnstartedServer(mux)
		ts.EnableHTTP2 = true
		ts.StartTLS()
		defer ts.Close()

		var out, errOut syncBuffer
		cmd := newTunnelCmdWithDeps(&App{}, &tunnelDeps{
			workDir:    t.TempDir(),
			httpClient: ts.Client(),
		})
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs([]string{
			"--server", ts.URL,
			"--service", "web",
			"--preview-id", "fail-start",
			"--json",
			"--",
			"/nonexistent-diverge-test-binary-xyz",
		})
		err := cmd.ExecuteContext(t.Context())
		require.Error(t, err)

		var sawErr bool
		for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			if line == "" {
				continue
			}
			var ev tunnelJSONEvent
			require.NoError(t, json.Unmarshal([]byte(line), &ev), "invalid NDJSON line: %s", line)
			if ev.Event == "error" && ev.Error != "" {
				sawErr = true
			}
		}
		assert.True(t, sawErr, "expected NDJSON error event on stdout, got: %s", out.String())
	})

	t.Run("tunnel list --json emits stderr JSON error on insecure HTTP", func(t *testing.T) {
		var listOut, listErr bytes.Buffer
		app := &App{}
		root := NewRootCmd(app)
		for _, sub := range root.Commands() {
			if sub.Name() == "tunnel" {
				root.RemoveCommand(sub)
				break
			}
		}
		root.AddCommand(newTunnelCmdWithDeps(app, &tunnelDeps{workDir: t.TempDir()}))
		root.SetOut(&listOut)
		root.SetErr(&listErr)
		root.SetArgs([]string{"tunnel", "list", "--json", "--server", "http://diverge.example.com:8081"})

		err := root.ExecuteContext(t.Context())
		require.Error(t, err)
		assert.ErrorIs(t, err, tunnelclient.ErrInsecureHTTP)
		assert.Empty(t, listOut.String())

		var errPayload map[string]string
		require.NoError(t, json.Unmarshal(bytes.TrimSpace(listErr.Bytes()), &errPayload), "expected valid JSON on stderr, got: %s", listErr.String())
		assert.Contains(t, errPayload["error"], "loopback")
	})
}
