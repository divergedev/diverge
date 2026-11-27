package tunnelclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

const (
	defaultCommandTokenTimeout = 15 * time.Second
	defaultMaxTokenStdout      = 64 << 10 // 64 KiB
	defaultMaxTokenStderr      = 4 << 10  // 4 KiB
)

// ErrNoCredential is returned when no credential can be resolved for the
// diverge server.
var ErrNoCredential = errors.New("no credential available for the diverge server")

// ErrInsecureHTTP is returned when credentials or tunnel streams target a non-loopback plaintext http:// URL.
var ErrInsecureHTTP = errors.New("insecure HTTP scheme is only allowed for loopback addresses")

// TokenSource provides a bearer credential for authenticating tunnel and control-plane requests.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// InvalidatableTokenSource is implemented by TokenSource types whose cached or
// stale credentials can be invalidated on 401/Unauthenticated responses.
type InvalidatableTokenSource interface {
	TokenSource
	Invalidate()
}

// StaticTokenSource provides a constant token string.
type StaticTokenSource string

// Token returns the trimmed static token or ErrNoCredential when empty.
func (s StaticTokenSource) Token(_ context.Context) (string, error) {
	tok := strings.TrimSpace(string(s))
	if tok == "" {
		return "", ErrNoCredential
	}
	if strings.ContainsAny(tok, "\r\n") {
		return "", errors.New("tunnel credential contains invalid newline characters")
	}
	return tok, nil
}

// FileTokenSource reads a token from a file path (such as a projected Kubernetes
// ServiceAccount token) and reloads it dynamically when the file's modification time changes.
type FileTokenSource struct {
	path        string
	mu          sync.RWMutex
	lastToken   string
	lastModTime time.Time
	lastCheck   time.Time
}

// NewFileTokenSource creates a FileTokenSource reading from the given path.
func NewFileTokenSource(path string) *FileTokenSource {
	return &FileTokenSource{path: path}
}

// Invalidate forces the next Token call to re-stat and re-read the token file.
func (f *FileTokenSource) Invalidate() {
	f.mu.Lock()
	f.lastToken = ""
	f.lastModTime = time.Time{}
	f.lastCheck = time.Time{}
	f.mu.Unlock()
}

// Token reads and returns the trimmed token from disk, rate-limiting stat checks.
func (f *FileTokenSource) Token(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	f.mu.RLock()
	// Rate-limit stat syscalls to at most once every 5 seconds under high load.
	if f.lastToken != "" && time.Since(f.lastCheck) < 5*time.Second {
		tok := f.lastToken
		f.mu.RUnlock()
		return tok, nil
	}
	f.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.lastToken != "" && time.Since(f.lastCheck) < 5*time.Second {
		return f.lastToken, nil
	}

	fi, err := os.Stat(f.path)
	if err != nil {
		return "", fmt.Errorf("failed to stat bearer token file %s: %w", f.path, err)
	}

	f.lastCheck = time.Now()

	// If the file on disk has not changed, keep using the cached token.
	if f.lastToken != "" && fi.ModTime().Equal(f.lastModTime) {
		return f.lastToken, nil
	}

	data, err := os.ReadFile(f.path)
	if err != nil {
		return "", fmt.Errorf("failed to read bearer token file %s: %w", f.path, err)
	}

	tok := strings.TrimSpace(string(data))
	if tok == "" {
		return "", fmt.Errorf("bearer token file %s is empty", f.path)
	}
	if strings.ContainsAny(tok, "\r\n") {
		return "", fmt.Errorf("bearer token file %s contains invalid newline characters", f.path)
	}

	f.lastToken = tok
	f.lastModTime = fi.ModTime()
	return tok, nil
}

// ChainTokenSource tries a sequence of TokenSource implementations in order,
// returning the first non-empty token found.
type ChainTokenSource struct {
	sources []TokenSource
}

// NewChainTokenSource creates a ChainTokenSource from the provided non-nil sources.
func NewChainTokenSource(sources ...TokenSource) *ChainTokenSource {
	valid := make([]TokenSource, 0, len(sources))
	for _, s := range sources {
		if s != nil {
			valid = append(valid, s)
		}
	}
	return &ChainTokenSource{sources: valid}
}

// Invalidate forwards invalidation to every wrapped source implementing InvalidatableTokenSource.
func (c *ChainTokenSource) Invalidate() {
	for _, s := range c.sources {
		if inv, ok := s.(InvalidatableTokenSource); ok {
			inv.Invalidate()
		}
	}
}

// Token returns the first non-empty token yielded by any configured source.
func (c *ChainTokenSource) Token(ctx context.Context) (string, error) {
	lastErr := ErrNoCredential
	for _, s := range c.sources {
		tok, err := s.Token(ctx)
		if err == nil && tok != "" {
			return tok, nil
		}
		if err != nil && !errors.Is(err, ErrNoCredential) {
			lastErr = err
		}
	}
	return "", lastErr
}

// SplitPOSIXCommand parses a command string into argv using POSIX-style single quotes,
// double quotes, and backslash escapes without invoking a shell. Unquoted shell
// metacharacters are rejected to make direct execution expectations explicit.
func SplitPOSIXCommand(cmdline string) ([]string, error) {
	var args []string
	var buf strings.Builder
	var inSingle, inDouble, escaped, hasToken bool

	for _, r := range cmdline {
		if inSingle {
			if r == '\'' {
				inSingle = false
				continue
			}
			buf.WriteRune(r)
			hasToken = true
			continue
		}

		if inDouble {
			if escaped {
				escaped = false
				hasToken = true
				switch r {
				case '\\', '"', '$', '`':
					buf.WriteRune(r)
				default:
					buf.WriteRune('\\')
					buf.WriteRune(r)
				}
				continue
			}
			switch r {
			case '"':
				inDouble = false
			case '\\':
				escaped = true
			default:
				buf.WriteRune(r)
				hasToken = true
			}
			continue
		}

		if escaped {
			escaped = false
			if r == '\n' || r == '\r' {
				return nil, fmt.Errorf("--token-cmd is executed directly without a shell and cannot contain shell metacharacter %q", r)
			}
			buf.WriteRune(r)
			hasToken = true
			continue
		}

		switch r {
		case ';', '|', '&', '<', '>', '`', '$', '(', ')', '\n', '\r':
			return nil, fmt.Errorf("--token-cmd is executed directly without a shell and cannot contain shell metacharacter %q", r)
		case '\'':
			inSingle = true
			hasToken = true
		case '"':
			inDouble = true
			hasToken = true
		case '\\':
			escaped = true
		default:
			if unicode.IsSpace(r) {
				if hasToken {
					args = append(args, buf.String())
					buf.Reset()
					hasToken = false
				}
				continue
			}
			buf.WriteRune(r)
			hasToken = true
		}
	}

	if inSingle || inDouble {
		return nil, errors.New("unterminated quote in --token-cmd")
	}
	if escaped {
		return nil, errors.New("unterminated escape in --token-cmd")
	}
	if hasToken {
		args = append(args, buf.String())
	}
	if len(args) == 0 || args[0] == "" {
		return nil, errors.New("--token-cmd must not be empty")
	}
	return args, nil
}

type cappedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.max <= 0 {
		b.overflow = true
		return len(p), nil
	}
	remaining := b.max - b.buf.Len()
	if remaining > 0 {
		n := min(len(p), remaining)
		_, _ = b.buf.Write(p[:n])
	}
	if len(p) > remaining {
		b.overflow = true
	}
	return len(p), nil
}

// CommandTokenSource executes an external command directly (without a shell) to
// obtain a bearer token. Calls are uncached across dial attempts and coalesced
// concurrently via singleflight.
type CommandTokenSource struct {
	args      []string
	timeout   time.Duration
	maxStdout int
	maxStderr int
	sf        singleflight.Group
}

// NewCommandTokenSource parses cmdline with SplitPOSIXCommand and returns a CommandTokenSource.
func NewCommandTokenSource(cmdline string) (*CommandTokenSource, error) {
	args, err := SplitPOSIXCommand(cmdline)
	if err != nil {
		return nil, err
	}
	return &CommandTokenSource{
		args:      args,
		timeout:   defaultCommandTokenTimeout,
		maxStdout: defaultMaxTokenStdout,
		maxStderr: defaultMaxTokenStderr,
	}, nil
}

// Invalidate implements InvalidatableTokenSource. CommandTokenSource is already
// uncached across dial attempts, so Invalidate is a no-op.
func (c *CommandTokenSource) Invalidate() {}

// Token executes the configured command using a detached timeout and coalesces
// concurrent invocations with singleflight.
func (c *CommandTokenSource) Token(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	ch := c.sf.DoChan("token", func() (any, error) {
		return c.execOnce()
	})

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return "", res.Err
		}
		tok, _ := res.Val.(string)
		return tok, nil
	}
}

func (c *CommandTokenSource) execOnce() (string, error) {
	execCtx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, c.args[0], c.args[1:]...) // #nosec G204 -- user-configured local credential helper, parsed without a shell
	cmd.WaitDelay = 500 * time.Millisecond

	stdoutBuf := &cappedBuffer{max: c.maxStdout}
	stderrBuf := &cappedBuffer{max: c.maxStderr}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	if err := cmd.Run(); err != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("token command timed out after %s", c.timeout)
		}
		errMsg := strings.TrimSpace(stderrBuf.buf.String())
		if errMsg != "" {
			return "", fmt.Errorf("token command failed (%w): %s", err, errMsg)
		}
		return "", fmt.Errorf("token command failed: %w", err)
	}

	if stdoutBuf.overflow {
		return "", fmt.Errorf("token command output exceeded maximum size (%d bytes)", c.maxStdout)
	}

	tok := strings.TrimSpace(stdoutBuf.buf.String())
	if tok == "" {
		return "", fmt.Errorf("%w: token command returned empty output", ErrNoCredential)
	}
	if strings.ContainsAny(tok, "\r\n") {
		return "", errors.New("token command output contains invalid newline characters")
	}
	return tok, nil
}

// IsLoopbackHost reports whether host (optionally with :port) resolves to localhost or a loopback IP.
func IsLoopbackHost(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		return false
	}
	if strings.Contains(h, ":") {
		if sh, _, err := net.SplitHostPort(h); err == nil {
			h = sh
		}
	}
	h = strings.Trim(h, "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// IsLoopbackAddress reports whether host (without a :port suffix) is localhost or a loopback IP.
func IsLoopbackAddress(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		return false
	}
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// AuthTransport attaches the bearer credential from TokenSource to every outbound request
// and refuses cleartext HTTP transmission to non-loopback hosts.
type AuthTransport struct {
	Base        http.RoundTripper
	TokenSource TokenSource
}

// RoundTrip clones the request with Authorization: Bearer when TokenSource is configured.
func (t *AuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if t.TokenSource == nil {
		return base.RoundTrip(req)
	}

	// Security: Do not transmit credentials in cleartext over non-loopback HTTP.
	if req.URL != nil && strings.EqualFold(req.URL.Scheme, "http") && !IsLoopbackHost(req.URL.Host) {
		return nil, fmt.Errorf("%w, got %q", ErrInsecureHTTP, req.URL.Host)
	}

	tok, err := t.TokenSource.Token(req.Context())
	if err != nil {
		return nil, fmt.Errorf("failed to get tunnel auth token: %w", err)
	}

	clone := req.Clone(req.Context())
	if tok != "" {
		clone.Header.Set("Authorization", "Bearer "+tok)
	}
	return base.RoundTrip(clone)
}

// BaseTransport returns an HTTP/2-capable RoundTripper for serverAddr. Plaintext http://
// URLs receive a prior-knowledge unencrypted HTTP/2 (h2c) transport because bidirectional
// ConnectRPC streams require HTTP/2.
func BaseTransport(serverAddr string) http.RoundTripper {
	u, err := url.Parse(strings.TrimSpace(serverAddr))
	if err != nil || !strings.EqualFold(u.Scheme, "http") {
		return http.DefaultTransport
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Protocols = new(http.Protocols)
	t.Protocols.SetUnencryptedHTTP2(true)
	return t
}

// NewHTTPClient constructs an *http.Client configured with AuthTransport and a strict
// cross-origin redirect guard that never sends credentials to a different host or scheme.
func NewHTTPClient(serverAddr string, tokenSource TokenSource, baseTransport http.RoundTripper) *http.Client {
	if baseTransport == nil {
		baseTransport = BaseTransport(serverAddr)
	}
	return &http.Client{
		Transport: &AuthTransport{
			Base:        baseTransport,
			TokenSource: tokenSource,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if len(via) > 0 {
				orig := via[0]
				if !strings.EqualFold(req.URL.Scheme, orig.URL.Scheme) || !strings.EqualFold(req.URL.Host, orig.URL.Host) {
					return fmt.Errorf("refusing to send credentials across redirects to different scheme or host: %s -> %s", orig.URL, req.URL)
				}
			}
			return nil
		},
	}
}

// ClientInstanceIDPath returns the per-machine client-id path under XDG_CONFIG_HOME or ~/.config.
func ClientInstanceIDPath() (string, error) {
	cfgRoot := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if cfgRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("could not determine user home directory")
		}
		cfgRoot = filepath.Join(home, ".config")
	}
	return filepath.Join(cfgRoot, "diverge", "client-id"), nil
}

// LoadOrCreateClientInstanceID returns the persistent client instance UUID from
// $XDG_CONFIG_HOME/diverge/client-id (or ~/.config/diverge/client-id), creating
// it with mode 0600 (0700 parent) when missing, or falling back to an ephemeral UUID.
func LoadOrCreateClientInstanceID() string {
	path, err := ClientInstanceIDPath()
	if err != nil {
		return uuid.NewString()
	}
	return LoadOrCreateClientInstanceIDAt(path)
}

// LoadOrCreateClientInstanceIDAt loads or creates a client instance UUID at path.
func LoadOrCreateClientInstanceIDAt(path string) string {
	if data, err := os.ReadFile(path); err == nil { // #nosec G304 -- trusted config path
		id := strings.TrimSpace(string(data))
		if id != "" && len(id) <= 128 && !strings.ContainsAny(id, "\r\n") {
			return id
		}
	}

	id := uuid.NewString()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return id
	}
	_ = os.Chmod(dir, 0o700)

	tmp, err := os.CreateTemp(dir, ".client-id-*")
	if err != nil {
		return id
	}
	tmpName := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return id
	}
	if _, err := tmp.WriteString(id + "\n"); err != nil {
		_ = tmp.Close()
		return id
	}
	if err := tmp.Close(); err != nil {
		return id
	}
	if err := os.Rename(tmpName, path); err != nil {
		return id
	}
	cleanupTmp = false
	return id
}
