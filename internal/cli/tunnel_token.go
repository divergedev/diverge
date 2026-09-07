package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
)

// tunnelTokenEnvVar overrides the credential used for the tunnel.
const tunnelTokenEnvVar = "DIVERGE_TOKEN"

// ErrNoTunnelCredential is returned when no credential can be resolved for the
// tunnel. The server authenticates every Tunnel RPC by Kubernetes TokenReview,
// so connecting without one only ever yields 401.
var ErrNoTunnelCredential = errors.New("no credential available for the diverge server")

// TokenSource provides a credential for authenticating tunnel connections.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticTokenSource provides a constant token string.
type StaticTokenSource string

func (s StaticTokenSource) Token(ctx context.Context) (string, error) {
	tok := strings.TrimSpace(string(s))
	if tok == "" {
		return "", ErrNoTunnelCredential
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

func (f *FileTokenSource) Token(ctx context.Context) (string, error) {
	f.mu.RLock()
	// Rate-limit stat syscalls to at most once every 5 seconds under high load
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

	// If the file on disk has not modified, keep using the cached token
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

	f.lastToken = tok
	f.lastModTime = fi.ModTime()
	return tok, nil
}

// OpenBaoTokenSource resolves credentials from OpenBao or HashiCorp Vault environment
// variables (BAO_TOKEN, VAULT_TOKEN) and home directory token files (~/.bao-token, ~/.vault-token).
type OpenBaoTokenSource struct{}

// NewOpenBaoTokenSource creates a token source for OpenBao/Vault.
func NewOpenBaoTokenSource() *OpenBaoTokenSource {
	return &OpenBaoTokenSource{}
}

func (o *OpenBaoTokenSource) Token(ctx context.Context) (string, error) {
	if tok := strings.TrimSpace(os.Getenv("BAO_TOKEN")); tok != "" {
		return tok, nil
	}
	if tok := strings.TrimSpace(os.Getenv("VAULT_TOKEN")); tok != "" {
		return tok, nil
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if data, err := os.ReadFile(filepath.Join(home, ".bao-token")); err == nil {
			if tok := strings.TrimSpace(string(data)); tok != "" {
				return tok, nil
			}
		}
		if data, err := os.ReadFile(filepath.Join(home, ".vault-token")); err == nil {
			if tok := strings.TrimSpace(string(data)); tok != "" {
				return tok, nil
			}
		}
	}

	return "", ErrNoTunnelCredential
}

type authCaptureKey struct{}

type authCapturingRoundTripper struct{}

func (c *authCapturingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if holder, ok := req.Context().Value(authCaptureKey{}).(*string); ok {
		*holder = req.Header.Get("Authorization")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}, nil
}

// KubeTokenSource leverages client-go's transport wrappers (including ExecProvider, AuthProvider,
// and token refreshers) to dynamically resolve bearer credentials from a Kubernetes rest.Config.
type KubeTokenSource struct {
	rt http.RoundTripper
}

// NewKubeTokenSource creates a KubeTokenSource from rest.Config.
func NewKubeTokenSource(restCfg *rest.Config) (*KubeTokenSource, error) {
	if restCfg == nil {
		return nil, errors.New("nil rest.Config")
	}
	conf, err := restCfg.TransportConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get transport config: %w", err)
	}

	capturer := &authCapturingRoundTripper{}
	rt, err := transport.HTTPWrappersForConfig(conf, capturer)
	if err != nil {
		return nil, fmt.Errorf("failed to build auth transport wrappers: %w", err)
	}
	return &KubeTokenSource{rt: rt}, nil
}

func (k *KubeTokenSource) Token(ctx context.Context) (string, error) {
	var authHeader string
	reqCtx := context.WithValue(ctx, authCaptureKey{}, &authHeader)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "https://kubernetes.default.svc", nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoTunnelCredential, err)
	}
	resp, err := k.rt.RoundTrip(req)
	if err != nil {
		return "", fmt.Errorf("%w: failed to resolve kubernetes token: %v", ErrNoTunnelCredential, err)
	}
	_ = resp.Body.Close()

	token := strings.TrimSpace(authHeader)
	if strings.HasPrefix(strings.ToLower(token), "bearer") {
		token = strings.TrimSpace(token[6:])
	}
	if token == "" {
		return "", ErrNoTunnelCredential
	}
	return token, nil
}

// ChainTokenSource tries a sequence of TokenSource implementations in order,
// returning the first non-empty token found.
type ChainTokenSource struct {
	sources []TokenSource
}

// NewChainTokenSource creates a ChainTokenSource from the provided sources.
func NewChainTokenSource(sources ...TokenSource) *ChainTokenSource {
	valid := make([]TokenSource, 0, len(sources))
	for _, s := range sources {
		if s != nil {
			valid = append(valid, s)
		}
	}
	return &ChainTokenSource{sources: valid}
}

func (c *ChainTokenSource) Token(ctx context.Context) (string, error) {
	var lastErr = ErrNoTunnelCredential
	for _, s := range c.sources {
		tok, err := s.Token(ctx)
		if err == nil && tok != "" {
			return tok, nil
		}
		if err != nil && !errors.Is(err, ErrNoTunnelCredential) {
			lastErr = err
		}
	}
	return "", lastErr
}

// resolveTunnelTokenSource resolves the credential to present to the diverge server in order:
// 1. Explicit token flag (--token)
// 2. DIVERGE_TOKEN environment variable
// 3. OpenBao / Vault token (BAO_TOKEN, VAULT_TOKEN, ~/.bao-token, ~/.vault-token)
// 4. Kubeconfig static bearer token
// 5. Kubeconfig bearer token file (with dynamic reload)
// 6. Kubeconfig dynamic ExecProvider / AuthProvider
func resolveTunnelTokenSource(explicit string, restCfg *rest.Config) (TokenSource, error) {
	if token := strings.TrimSpace(explicit); token != "" {
		return StaticTokenSource(token), nil
	}
	if token := strings.TrimSpace(os.Getenv(tunnelTokenEnvVar)); token != "" {
		return StaticTokenSource(token), nil
	}

	sources := []TokenSource{
		NewOpenBaoTokenSource(),
	}

	if restCfg != nil {
		if token := strings.TrimSpace(restCfg.BearerToken); token != "" {
			sources = append(sources, StaticTokenSource(token))
		}
		if restCfg.BearerTokenFile != "" {
			sources = append(sources, NewFileTokenSource(restCfg.BearerTokenFile))
		}
		if kubeTS, err := NewKubeTokenSource(restCfg); err == nil {
			sources = append(sources, kubeTS)
		}
	}

	chain := NewChainTokenSource(sources...)
	// Early validation: ensure at least one source can provide a valid credential right now
	tok, err := chain.Token(context.Background())
	if err != nil || tok == "" {
		if err == nil {
			err = ErrNoTunnelCredential
		}
		return nil, err
	}

	return chain, nil
}

// resolveTunnelToken is a convenience wrapper returning the string token from resolveTunnelTokenSource.
func resolveTunnelToken(explicit string, restCfg *rest.Config) (string, error) {
	ts, err := resolveTunnelTokenSource(explicit, restCfg)
	if err != nil {
		return "", err
	}
	return ts.Token(context.Background())
}
