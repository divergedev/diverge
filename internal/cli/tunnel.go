package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/util/validation"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/internal/deployer"
	"github.com/divergedev/diverge/internal/git"
	"github.com/divergedev/diverge/internal/tunnelclient"
)

const (
	userConfigVersion          = 1
	defaultTunnelPort          = 8080
	defaultWaitReadyTimeout    = 60 * time.Second
	tunnelShutdownDrainTimeout = 3 * time.Second
	maxTunnelDefaultsBytes     = 5 << 20
	maxTunnelMetadataEntries   = 16
	maxTunnelMetadataKeyLen    = 64
	maxTunnelMetadataValueLen  = 256
)

// UserConfig represents the non-sensitive user preferences stored in
// $XDG_CONFIG_HOME/diverge/config.yaml (or ~/.config/diverge/config.yaml).
type UserConfig struct {
	Version          int    `yaml:"version"`
	Server           string `yaml:"server"`
	DefaultNamespace string `yaml:"default_namespace"`
}

// TunnelDefaults holds strictly validated, non-privileged defaults loaded from a
// repository-local .diverge.yaml file. Credential commands, token files, servers,
// and local_host are never loaded from repository configuration.
type TunnelDefaults struct {
	Service   string
	Namespace string
	Port      int
}

type tunnelDeps struct {
	detector       EnvironmentDetector
	userConfigPath string
	workDir        string
	baseTransport  http.RoundTripper
	httpClient     *http.Client
	initialBackoff time.Duration
}

// UserConfigPath returns the path to the user-owned diverge CLI config file.
func UserConfigPath() (string, error) {
	cfgRoot := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if cfgRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("could not determine user home directory")
		}
		cfgRoot = filepath.Join(home, ".config")
	}
	return filepath.Join(cfgRoot, "diverge", "config.yaml"), nil
}

// LoadUserConfig reads user preferences from the default user config path.
// A missing file returns an empty UserConfig without error.
func LoadUserConfig() (*UserConfig, error) {
	path, err := UserConfigPath()
	if err != nil {
		return &UserConfig{}, nil
	}
	return LoadUserConfigFrom(path)
}

// LoadUserConfigFrom reads user preferences from path.
func LoadUserConfigFrom(path string) (*UserConfig, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- user-owned config file
	if err != nil {
		if os.IsNotExist(err) {
			return &UserConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read user config %s: %w", path, err)
	}
	var cfg UserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse user config %s: %w", path, err)
	}
	if cfg.Version != 0 && cfg.Version != userConfigVersion {
		return nil, fmt.Errorf("unsupported user config version %d in %s (expected %d)", cfg.Version, path, userConfigVersion)
	}
	cfg.Server = strings.TrimSpace(cfg.Server)
	cfg.DefaultNamespace = strings.TrimSpace(cfg.DefaultNamespace)
	return &cfg, nil
}

// LoadTunnelDefaults reads non-sensitive defaults from .diverge.yaml in dir,
// supporting both `kind: ServicePreview` and `version: "1"` config schemas.
// Untrusted repository keys (token_cmd, token, token_file, server, local_host)
// are never read or honored.
func LoadTunnelDefaults(dir string) (TunnelDefaults, error) {
	if dir == "" {
		return TunnelDefaults{}, nil
	}
	cfgPath := filepath.Join(dir, ".diverge.yaml")
	f, err := os.Open(cfgPath) // #nosec G304 -- project-local .diverge.yaml in working dir
	if err != nil {
		if os.IsNotExist(err) {
			return TunnelDefaults{}, nil
		}
		return TunnelDefaults{}, fmt.Errorf("failed to read %s: %w", cfgPath, err)
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, maxTunnelDefaultsBytes+1))
	if err != nil {
		return TunnelDefaults{}, fmt.Errorf("failed to read %s: %w", cfgPath, err)
	}
	if len(data) > maxTunnelDefaultsBytes {
		return TunnelDefaults{}, fmt.Errorf("failed to read %s: exceeds %d byte limit", cfgPath, maxTunnelDefaultsBytes)
	}

	var header struct {
		Kind    string `yaml:"kind"`
		Version string `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return TunnelDefaults{}, fmt.Errorf("failed to parse %s: %w", cfgPath, err)
	}

	if header.Kind == "ServicePreview" {
		svcCfg, err := deployer.ParseDotDivergeConfig(data)
		if err != nil {
			return TunnelDefaults{}, err
		}
		return TunnelDefaults{
			Service:   strings.TrimSpace(svcCfg.Spec.ServiceName),
			Namespace: strings.TrimSpace(svcCfg.Spec.Namespace),
			Port:      int(svcCfg.Spec.Port),
		}, nil
	}

	if header.Version == "1" {
		// Decode strictly the safe subset of version: "1" fields so arbitrary extra keys
		// in an untrusted repo file cannot poison CLI execution or break decoding.
		var safeV1 struct {
			Services map[string]struct{} `yaml:"services"`
			Defaults struct {
				Routing struct {
					BaselineNamespace string `yaml:"baseline_namespace"`
				} `yaml:"routing"`
			} `yaml:"defaults"`
		}
		if err := yaml.Unmarshal(data, &safeV1); err != nil {
			return TunnelDefaults{}, fmt.Errorf("failed to parse %s: %w", cfgPath, err)
		}
		var out TunnelDefaults
		out.Namespace = strings.TrimSpace(safeV1.Defaults.Routing.BaselineNamespace)
		if len(safeV1.Services) == 1 {
			for svc := range safeV1.Services {
				out.Service = strings.TrimSpace(svc)
			}
		}
		return out, nil
	}

	return TunnelDefaults{}, nil
}

type tunnelRunOptions struct {
	Server           string
	Namespace        string
	Service          string
	PreviewID        string
	Port             int
	LocalHost        string
	Meta             []string
	Force            bool
	SkipK8sService   bool
	WaitReady        bool
	WaitReadyTimeout time.Duration
	TokenCmd         string
	TokenFile        string
	JSON             bool
	Args             []string
}

type tunnelListOptions struct {
	Server    string
	Namespace string
	PreviewID string
	Service   string
	TokenCmd  string
	TokenFile string
	JSON      bool
}

type tunnelJSONEvent struct {
	Event           string `json:"event"`
	TunnelID        string `json:"tunnel_id,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
	PreviewID       string `json:"preview_id,omitempty"`
	Service         string `json:"service,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	ProxyService    string `json:"proxy_service,omitempty"`
	K8sServiceName  string `json:"k8s_service_name,omitempty"`
	ProtocolVersion int32  `json:"protocol_version,omitempty"`
	Error           string `json:"error,omitempty"`
	BackoffMS       int64  `json:"backoff_ms,omitempty"`
	Code            string `json:"code,omitempty"`
	Reason          string `json:"reason,omitempty"`
	ExitCode        *int   `json:"exit_code,omitempty"`
	Timestamp       string `json:"timestamp"`
}

type ndjsonEmitter struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func newNDJSONEmitter(w io.Writer) *ndjsonEmitter {
	return &ndjsonEmitter{enc: json.NewEncoder(w)}
}

func (e *ndjsonEmitter) Emit(ev tunnelJSONEvent) {
	if e == nil {
		return
	}
	if ev.Timestamp == "" {
		ev.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	e.mu.Lock()
	_ = e.enc.Encode(ev)
	e.mu.Unlock()
}

func newTunnelCmd(app *App) *cobra.Command {
	return newTunnelCmdWithDeps(app, nil)
}

func newTunnelCmdWithDeps(app *App, deps *tunnelDeps) *cobra.Command {
	opts := tunnelRunOptions{
		LocalHost:        "127.0.0.1",
		WaitReadyTimeout: defaultWaitReadyTimeout,
	}

	cmd := &cobra.Command{
		Use:          "tunnel [-- <command> [args...]]",
		Short:        "Expose a local service port via the Diverge server without kubeconfig",
		SilenceUsage: true,
		Long: `Connect a local HTTP service directly to a remote diverge-server without
requiring a kubeconfig or cluster RBAC credentials. Optionally supervise a local
child process after '--' and emit structured NDJSON events with --json.

When supervising a child command, Unix builds terminate the entire child
process group on shutdown or exit, whereas Windows terminates only the
direct child process.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.JSON {
				cmd.SilenceErrors = true
			}
			if (cmd.ArgsLenAtDash() == -1 && len(args) > 0) || cmd.ArgsLenAtDash() > 0 {
				err := fmt.Errorf("unexpected arguments %v (use '-- <command> [args...]' to supervise a child command)", args)
				if opts.JSON {
					newNDJSONEmitter(cmd.OutOrStdout()).Emit(tunnelJSONEvent{Event: "error", Error: err.Error()})
				}
				return err
			}
			opts.Namespace = strings.TrimSpace(app.Namespace)
			opts.Args = args
			return runTunnel(cmd.Context(), cmd, opts, deps)
		},
	}

	// Persistent -n/--namespace is already registered on RootCmd via app.Namespace.
	cmd.Flags().StringVar(&opts.Server, "server", "", "Diverge server URL (default: $DIVERGE_SERVER or ~/.config/diverge/config.yaml)")
	cmd.Flags().StringVar(&opts.Service, "service", "", "Service name (default: .diverge.yaml or current directory name)")
	cmd.Flags().StringVar(&opts.PreviewID, "preview-id", "", "Preview ID (default: slugified <user>-<git-branch>)")
	cmd.Flags().IntVar(&opts.Port, "port", 0, "Local service port (default: .diverge.yaml or 8080)")
	cmd.Flags().StringVar(&opts.LocalHost, "local-host", "127.0.0.1", "Local loopback host to forward tunnel traffic to")
	cmd.Flags().StringArrayVar(&opts.Meta, "meta", nil, "Tunnel metadata entry as key=value (repeatable)")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "Evict any active conflicting tunnel for the same target")
	cmd.Flags().BoolVar(&opts.SkipK8sService, "skip-k8s-service", false, "Do not create a per-tunnel Kubernetes Service")
	cmd.Flags().BoolVar(&opts.WaitReady, "wait-ready", false, "Wait until the local TCP port is listening before registering the tunnel")
	cmd.Flags().DurationVar(&opts.WaitReadyTimeout, "wait-ready-timeout", defaultWaitReadyTimeout, "Maximum time to wait for --wait-ready")
	cmd.Flags().StringVar(&opts.TokenCmd, "token-cmd", "", "External command executed directly (without a shell) to fetch a bearer token")
	cmd.Flags().StringVar(&opts.TokenFile, "token-file", "", "File containing a bearer token for authenticating with diverge-server")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Emit NDJSON lifecycle events exclusively on stdout")

	cmd.AddCommand(newTunnelListCmdWithDeps(app, deps))
	return cmd
}

func newTunnelListCmdWithDeps(app *App, deps *tunnelDeps) *cobra.Command {
	var opts tunnelListOptions
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List active tunnels visible on the Diverge server",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.JSON {
				cmd.SilenceErrors = true
			}
			opts.Namespace = strings.TrimSpace(app.Namespace)
			return runTunnelList(cmd.Context(), cmd, opts, deps)
		},
	}

	cmd.Flags().StringVar(&opts.Server, "server", "", "Diverge server URL (default: $DIVERGE_SERVER or ~/.config/diverge/config.yaml)")
	cmd.Flags().StringVar(&opts.PreviewID, "preview-id", "", "Filter by preview ID")
	cmd.Flags().StringVar(&opts.Service, "service", "", "Filter by service name")
	cmd.Flags().StringVar(&opts.TokenCmd, "token-cmd", "", "External command executed directly (without a shell) to fetch a bearer token")
	cmd.Flags().StringVar(&opts.TokenFile, "token-file", "", "File containing a bearer token for authenticating with diverge-server")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output active tunnels as a JSON array")
	return cmd
}

func resolveServerAddr(explicit string, userCfg *UserConfig) (string, error) {
	addr := strings.TrimSpace(explicit)
	if addr == "" {
		addr = strings.TrimSpace(os.Getenv("DIVERGE_SERVER"))
	}
	if addr == "" && userCfg != nil {
		addr = strings.TrimSpace(userCfg.Server)
	}
	if addr == "" {
		return "", errors.New("server address is required: pass --server, set DIVERGE_SERVER, or configure ~/.config/diverge/config.yaml")
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("invalid server URL %q: expected http:// or https:// URL", addr)
	}
	if u.Scheme == "http" && !tunnelclient.IsLoopbackHost(u.Host) {
		return "", fmt.Errorf("%w: %s", tunnelclient.ErrInsecureHTTP, addr)
	}
	return strings.TrimRight(addr, "/"), nil
}

func parseMetadataFlags(entries []string) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	if len(entries) > maxTunnelMetadataEntries {
		return nil, fmt.Errorf("too many --meta entries (%d > %d)", len(entries), maxTunnelMetadataEntries)
	}
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		k, v, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --meta %q: expected key=value", entry)
		}
		k = strings.TrimSpace(k)
		if k == "" || len(k) > maxTunnelMetadataKeyLen {
			return nil, fmt.Errorf("invalid --meta key %q: must be 1-%d characters", k, maxTunnelMetadataKeyLen)
		}
		if len(v) > maxTunnelMetadataValueLen {
			return nil, fmt.Errorf("invalid --meta value for %q: exceeds %d characters", k, maxTunnelMetadataValueLen)
		}
		out[k] = v
	}
	return out, nil
}

func loadUserConfigWithDeps(deps *tunnelDeps) (*UserConfig, error) {
	if deps != nil && deps.userConfigPath != "" {
		return LoadUserConfigFrom(deps.userConfigPath)
	}
	return LoadUserConfig()
}

func runTunnel(ctx context.Context, cmd *cobra.Command, opts tunnelRunOptions, deps *tunnelDeps) error {
	if deps == nil {
		deps = &tunnelDeps{}
	}
	detector := deps.detector
	if detector == nil {
		detector = &DefaultEnvironmentDetector{}
	}

	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	var jsonOut *ndjsonEmitter
	childStdout := out
	childStderr := errOut
	logLevel := slog.LevelInfo
	if opts.JSON {
		cmd.SilenceErrors = true
		jsonOut = newNDJSONEmitter(out)
		childStdout = errOut
		childStderr = errOut
		logLevel = slog.LevelWarn
	}
	failEarly := func(err error) error {
		if opts.JSON && err != nil {
			jsonOut.Emit(tunnelJSONEvent{Event: "error", Error: err.Error()})
		}
		return err
	}

	if opts.WaitReady && opts.WaitReadyTimeout <= 0 {
		return failEarly(errors.New("--wait-ready-timeout must be greater than 0"))
	}

	userCfg, err := loadUserConfigWithDeps(deps)
	if err != nil {
		return failEarly(err)
	}
	serverAddr, err := resolveServerAddr(opts.Server, userCfg)
	if err != nil {
		return failEarly(err)
	}

	workDir := deps.workDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	repoDefaults, err := LoadTunnelDefaults(workDir)
	if err != nil {
		return failEarly(err)
	}

	namespace := strings.TrimSpace(opts.Namespace)
	if namespace == "" {
		namespace = repoDefaults.Namespace
	}
	if namespace == "" {
		namespace = userCfg.DefaultNamespace
	}
	if namespace != "" {
		if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
			return failEarly(fmt.Errorf("namespace %q is not a valid DNS label: %s", namespace, strings.Join(errs, "; ")))
		}
	}

	service := strings.TrimSpace(opts.Service)
	if service == "" {
		service = repoDefaults.Service
	}
	if service == "" && workDir != "" {
		service = git.SlugifyBranch(filepath.Base(workDir))
	}
	if errs := validation.IsDNS1123Label(service); service == "" || len(errs) > 0 {
		return failEarly(fmt.Errorf("service %q is not a valid DNS label: use --service", service))
	}

	previewID := strings.TrimSpace(opts.PreviewID)
	if previewID == "" {
		username, _ := detector.DetectUsername(ctx)
		if username == "" {
			username = "dev"
		}
		branch, err := detector.DetectGitBranch(ctx)
		if err != nil || branch == "" {
			branch = "local"
		}
		previewID = git.SlugifyBranch(username + "-" + branch)
	}
	if errs := validation.IsDNS1123Label(previewID); len(errs) > 0 {
		return failEarly(fmt.Errorf("preview-id %q is not a valid DNS label: %s", previewID, strings.Join(errs, "; ")))
	}

	port := opts.Port
	if port == 0 {
		port = repoDefaults.Port
	}
	if port == 0 {
		port = defaultTunnelPort
	}
	if port < 1 || port > 65535 {
		return failEarly(fmt.Errorf("--port must be between 1 and 65535, got %d", port))
	}

	localHost := strings.TrimSpace(opts.LocalHost)
	if localHost == "" {
		localHost = "127.0.0.1"
	}
	if !tunnelclient.IsLoopbackAddress(localHost) {
		return failEarly(fmt.Errorf("--local-host %q must be a loopback address", localHost))
	}

	metadata, err := parseMetadataFlags(opts.Meta)
	if err != nil {
		return failEarly(err)
	}

	tokenSource, err := resolveStandaloneTunnelTokenSource(ctx, opts.TokenCmd, opts.TokenFile)
	if err != nil {
		return failEarly(err)
	}
	logger := slog.New(slog.NewTextHandler(errOut, &slog.HandlerOptions{Level: logLevel}))

	baseTransport := deps.baseTransport
	if baseTransport == nil {
		baseTransport = tunnelclient.BaseTransport(serverAddr)
	}

	var tunnelHTTPClient *http.Client
	if deps.httpClient != nil {
		tunnelHTTPClient = &http.Client{
			Transport: &tunnelclient.AuthTransport{
				Base:        deps.httpClient.Transport,
				TokenSource: tokenSource,
			},
			CheckRedirect: deps.httpClient.CheckRedirect,
		}
	}

	clientCfg := tunnelclient.Config{
		ServerAddr:       serverAddr,
		LocalHost:        localHost,
		LocalPort:        port,
		PreviewID:        previewID,
		Service:          service,
		Namespace:        namespace,
		Metadata:         metadata,
		Force:            opts.Force,
		SkipK8sService:   opts.SkipK8sService,
		ClientInstanceID: tunnelclient.LoadOrCreateClientInstanceID(),
		TokenSource:      tokenSource,
		BaseTransport:    baseTransport,
		TunnelHTTPClient: tunnelHTTPClient,
		InitialBackoff:   deps.initialBackoff,
		Logger:           logger,
		Events: tunnelclient.EventHandlers{
			OnReady: func(info tunnelclient.ReadyInfo) {
				if opts.JSON {
					jsonOut.Emit(tunnelJSONEvent{
						Event:           "ready",
						TunnelID:        info.TunnelID,
						Namespace:       namespace,
						PreviewID:       previewID,
						Service:         service,
						Endpoint:        info.Endpoint,
						ProxyService:    info.ProxyService,
						K8sServiceName:  info.K8sServiceName,
						ProtocolVersion: info.ProtocolVersion,
					})
					return
				}
				_, _ = fmt.Fprintf(out, "🚇 Tunnel active (id: %s, preview: %s, service: %s, endpoint: %s)\n",
					info.TunnelID, previewID, service, info.Endpoint)
			},
			OnReconnecting: func(rErr error, backoff time.Duration) {
				if opts.JSON {
					jsonOut.Emit(tunnelJSONEvent{
						Event:     "reconnecting",
						Error:     rErr.Error(),
						BackoffMS: backoff.Milliseconds(),
					})
					return
				}
				_, _ = fmt.Fprintf(errOut, "⚠️  Tunnel disconnected: %v (reconnecting in %v)\n", rErr, backoff)
			},
			OnClosed: func(code pb.CloseCode, reason string) {
				if opts.JSON {
					jsonOut.Emit(tunnelJSONEvent{
						Event:  "closed",
						Code:   code.String(),
						Reason: reason,
					})
					return
				}
				_, _ = fmt.Fprintf(errOut, "🚇 Tunnel closed by server (%s): %s\n", code.String(), reason)
			},
			OnError: func(fErr error) {
				if opts.JSON {
					jsonOut.Emit(tunnelJSONEvent{
						Event: "error",
						Error: fErr.Error(),
					})
					return
				}
				_, _ = fmt.Fprintf(errOut, "❌ Tunnel error: %v\n", fErr)
			},
		},
	}

	tc, err := tunnelclient.New(clientCfg)
	if err != nil {
		return failEarly(err)
	}

	if len(opts.Args) == 0 {
		if opts.WaitReady {
			if _, waitErr := waitForLocalPort(ctx, localHost, port, opts.WaitReadyTimeout, nil); waitErr != nil {
				if opts.JSON {
					jsonOut.Emit(tunnelJSONEvent{Event: "error", Error: waitErr.Error()})
				}
				return waitErr
			}
		}
		return failEarly(tc.ConnectWithRetry(ctx))
	}

	sessionCtx, sessionCancel := context.WithCancel(ctx)
	defer sessionCancel()

	var childExitEmitted atomic.Bool
	childErrCh := make(chan error, 1)
	sup := NewSupervisorWithOptions(
		opts.Args,
		nil,
		WithExitOnChildExit(true),
		WithSupervisorOutput(errOut),
		WithSupervisorChildIO(childStdout, childStderr),
		WithOnChildExit(func(code int, cErr error) {
			childExitEmitted.Store(true)
			if opts.JSON {
				errStr := ""
				if cErr != nil {
					errStr = cErr.Error()
				}
				c := code
				jsonOut.Emit(tunnelJSONEvent{
					Event:    "child_exited",
					ExitCode: &c,
					Error:    errStr,
				})
			}
		}),
	)
	go func() {
		childErrCh <- sup.Run(sessionCtx)
	}()

	if opts.WaitReady {
		childExited, waitErr := waitForLocalPort(sessionCtx, localHost, port, opts.WaitReadyTimeout, childErrCh)
		if waitErr != nil || childExited {
			sessionCancel()
			if !childExited {
				<-childErrCh
			}
			if waitErr != nil && opts.JSON && !childExitEmitted.Load() {
				jsonOut.Emit(tunnelJSONEvent{Event: "error", Error: waitErr.Error()})
			}
			return waitErr
		}
	}

	tunnelErrCh := make(chan error, 1)
	go func() {
		tunnelErrCh <- tc.ConnectWithRetry(sessionCtx)
	}()

	select {
	case cErr := <-childErrCh:
		sessionCancel()
		drainTimer := time.NewTimer(tunnelShutdownDrainTimeout)
		select {
		case <-tunnelErrCh:
		case <-drainTimer.C:
		}
		drainTimer.Stop()
		if cErr != nil && opts.JSON && !childExitEmitted.Load() {
			jsonOut.Emit(tunnelJSONEvent{Event: "error", Error: cErr.Error()})
		}
		return cErr

	case tErr := <-tunnelErrCh:
		sessionCancel()
		<-childErrCh
		if tErr != nil && opts.JSON {
			jsonOut.Emit(tunnelJSONEvent{Event: "error", Error: tErr.Error()})
		}
		return tErr

	case <-ctx.Done():
		sessionCancel()
		<-childErrCh
		drainTimer := time.NewTimer(tunnelShutdownDrainTimeout)
		select {
		case <-tunnelErrCh:
		case <-drainTimer.C:
		}
		drainTimer.Stop()
		return nil
	}
}

func waitForLocalPort(ctx context.Context, host string, port int, timeout time.Duration, childErrCh <-chan error) (bool, error) {
	if timeout <= 0 {
		timeout = defaultWaitReadyTimeout
	}
	addr := net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
	deadline := time.Now().Add(timeout)
	dialer := &net.Dialer{Timeout: 250 * time.Millisecond}

	for {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return false, nil
		}

		if childErrCh != nil {
			select {
			case cErr := <-childErrCh:
				return true, cErr
			default:
			}
		}

		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if timeout > 0 && time.Now().After(deadline) {
			return false, fmt.Errorf("timed out waiting for %s to accept connections after %s", addr, timeout)
		}

		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case cErr := <-childErrCh:
			timer.Stop()
			return true, cErr
		case <-timer.C:
		}
	}
}

type tunnelListItem struct {
	TunnelID  string            `json:"tunnel_id"`
	Namespace string            `json:"namespace"`
	PreviewID string            `json:"preview_id"`
	Service   string            `json:"service"`
	Port      int32             `json:"port"`
	Owner     string            `json:"owner"`
	HolderPod string            `json:"holder_pod,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	StartedAt string            `json:"started_at,omitempty"`
	ExpiresAt string            `json:"expires_at,omitempty"`
}

func runTunnelList(ctx context.Context, cmd *cobra.Command, opts tunnelListOptions, deps *tunnelDeps) error {
	if deps == nil {
		deps = &tunnelDeps{}
	}
	fail := func(err error) error {
		if opts.JSON && err != nil {
			_ = json.NewEncoder(cmd.ErrOrStderr()).Encode(map[string]string{"error": err.Error()})
		}
		return err
	}

	userCfg, err := loadUserConfigWithDeps(deps)
	if err != nil {
		return fail(err)
	}
	serverAddr, err := resolveServerAddr(opts.Server, userCfg)
	if err != nil {
		return fail(err)
	}

	tokenSource, err := resolveStandaloneTunnelTokenSource(ctx, opts.TokenCmd, opts.TokenFile)
	if err != nil {
		return fail(err)
	}

	var httpClient *http.Client
	if deps.httpClient != nil {
		httpClient = &http.Client{
			Transport: &tunnelclient.AuthTransport{
				Base:        deps.httpClient.Transport,
				TokenSource: tokenSource,
			},
			CheckRedirect: deps.httpClient.CheckRedirect,
		}
	} else {
		baseRT := deps.baseTransport
		if baseRT == nil {
			baseRT = tunnelclient.BaseTransport(serverAddr)
		}
		httpClient = tunnelclient.NewHTTPClient(serverAddr, tokenSource, baseRT)
	}

	resp, err := tunnelclient.ListTunnels(ctx, httpClient, serverAddr, &pb.ListTunnelsRequest{
		Namespace: strings.TrimSpace(opts.Namespace),
		PreviewId: strings.TrimSpace(opts.PreviewID),
		Service:   strings.TrimSpace(opts.Service),
	})
	if err != nil {
		return fail(fmt.Errorf("failed to list tunnels: %w", err))
	}

	out := cmd.OutOrStdout()
	if opts.JSON {
		items := make([]tunnelListItem, 0, len(resp.GetTunnels()))
		for _, t := range resp.GetTunnels() {
			item := tunnelListItem{
				TunnelID:  t.TunnelId,
				Namespace: t.Namespace,
				PreviewID: t.PreviewId,
				Service:   t.Service,
				Port:      t.Port,
				Owner:     t.Owner,
				HolderPod: t.HolderPod,
				Metadata:  t.Metadata,
			}
			if t.StartedAt != nil {
				item.StartedAt = t.StartedAt.AsTime().UTC().Format(time.RFC3339)
			}
			if t.ExpiresAt != nil {
				item.ExpiresAt = t.ExpiresAt.AsTime().UTC().Format(time.RFC3339)
			}
			items = append(items, item)
		}
		enc := json.NewEncoder(out)
		return enc.Encode(items)
	}

	tunnels := resp.GetTunnels()
	if len(tunnels) == 0 {
		_, _ = fmt.Fprintln(out, "No active tunnels found.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(w, "TUNNEL ID\tNAMESPACE\tPREVIEW\tSERVICE\tPORT\tOWNER\tEXPIRES")
	for _, t := range tunnels {
		expires := "-"
		if t.ExpiresAt != nil {
			expires = t.ExpiresAt.AsTime().UTC().Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			t.TunnelId, t.Namespace, t.PreviewId, t.Service, t.Port, t.Owner, expires)
	}
	return w.Flush()
}
