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

	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"

	"github.com/divergedev/diverge/internal/tunnelclient"
)

// tunnelTokenEnvVar overrides the credential used for the tunnel.
const tunnelTokenEnvVar = "DIVERGE_TOKEN"

// ErrNoTunnelCredential is returned when no credential can be resolved for the
// tunnel.
var ErrNoTunnelCredential = tunnelclient.ErrNoCredential

// TokenSource aliases tunnelclient.TokenSource for CLI callers.
type TokenSource = tunnelclient.TokenSource

// InvalidatableTokenSource aliases tunnelclient.InvalidatableTokenSource.
type InvalidatableTokenSource = tunnelclient.InvalidatableTokenSource

// StaticTokenSource aliases tunnelclient.StaticTokenSource.
type StaticTokenSource = tunnelclient.StaticTokenSource

// FileTokenSource aliases tunnelclient.FileTokenSource.
type FileTokenSource = tunnelclient.FileTokenSource

// ChainTokenSource aliases tunnelclient.ChainTokenSource.
type ChainTokenSource = tunnelclient.ChainTokenSource

// CommandTokenSource aliases tunnelclient.CommandTokenSource.
type CommandTokenSource = tunnelclient.CommandTokenSource

// NewFileTokenSource creates a FileTokenSource reading from path.
func NewFileTokenSource(path string) *FileTokenSource {
	return tunnelclient.NewFileTokenSource(path)
}

// NewChainTokenSource creates a ChainTokenSource from sources.
func NewChainTokenSource(sources ...TokenSource) *ChainTokenSource {
	return tunnelclient.NewChainTokenSource(sources...)
}

// NewCommandTokenSource creates an uncached CommandTokenSource from cmdline.
func NewCommandTokenSource(cmdline string) (*CommandTokenSource, error) {
	return tunnelclient.NewCommandTokenSource(cmdline)
}

// OpenBaoTokenSource resolves credentials from OpenBao or HashiCorp Vault environment
// variables (BAO_TOKEN, VAULT_TOKEN) and home directory token files (~/.bao-token, ~/.vault-token).
// Note: This source is for application secrets and custom integrations; it is not used in
// resolveTunnelTokenSource because the Diverge server authenticates tunnels via Kubernetes TokenReview or OIDC.
type OpenBaoTokenSource struct{}

// NewOpenBaoTokenSource creates a token source for OpenBao/Vault.
func NewOpenBaoTokenSource() *OpenBaoTokenSource {
	return &OpenBaoTokenSource{}
}

// Token reads credentials from OpenBao/Vault environment variables or home token files.
func (o *OpenBaoTokenSource) Token(_ context.Context) (string, error) {
	if tok := strings.TrimSpace(os.Getenv("BAO_TOKEN")); tok != "" {
		return tok, nil
	}
	if tok := strings.TrimSpace(os.Getenv("VAULT_TOKEN")); tok != "" {
		return tok, nil
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if data, err := os.ReadFile(filepath.Join(home, ".bao-token")); err == nil { // #nosec G304 -- user home token file
			if tok := strings.TrimSpace(string(data)); tok != "" {
				return tok, nil
			}
		}
		if data, err := os.ReadFile(filepath.Join(home, ".vault-token")); err == nil { // #nosec G304 -- user home token file
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

// Token extracts the Kubernetes bearer credential by running the client-go auth wrapper.
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

// resolveTunnelTokenSource resolves the credential to present to the diverge server in order:
// 1. Explicit token flag (--token)
// 2. DIVERGE_TOKEN environment variable
// 3. Kubeconfig bearer token file (with dynamic reload)
// 4. Kubeconfig static bearer token
// 5. Kubeconfig dynamic ExecProvider / AuthProvider
//
// Note: OpenBao/Vault tokens are not in this chain because the Diverge server authenticates
// tunnel connections via Kubernetes TokenReview.
func resolveTunnelTokenSource(explicit string, restCfg *rest.Config) (TokenSource, error) {
	if token := strings.TrimSpace(explicit); token != "" {
		src := StaticTokenSource(token)
		if _, err := src.Token(context.Background()); err != nil {
			return nil, err
		}
		return src, nil
	}
	if token := strings.TrimSpace(os.Getenv(tunnelTokenEnvVar)); token != "" {
		src := StaticTokenSource(token)
		if _, err := src.Token(context.Background()); err != nil {
			return nil, err
		}
		return src, nil
	}

	var sources []TokenSource

	if restCfg != nil {
		// File-backed tokens take precedence over static BearerToken to allow dynamic token reload.
		if restCfg.BearerTokenFile != "" {
			sources = append(sources, NewFileTokenSource(restCfg.BearerTokenFile))
		}
		if token := strings.TrimSpace(restCfg.BearerToken); token != "" {
			sources = append(sources, StaticTokenSource(token))
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

// resolveStandaloneTunnelTokenSource resolves credentials for kubeconfig-free
// `diverge tunnel` / `diverge tunnel list` sessions in strict precedence:
// 1. --token-cmd (uncached POSIX command, mutually exclusive with --token-file)
// 2. --token-file (dynamic reloadable token file)
// 3. DIVERGE_TOKEN environment variable
func resolveStandaloneTunnelTokenSource(ctx context.Context, tokenCmd, tokenFile string) (TokenSource, error) {
	cmdStr := strings.TrimSpace(tokenCmd)
	fileStr := strings.TrimSpace(tokenFile)
	if cmdStr != "" && fileStr != "" {
		return nil, errors.New("--token-cmd and --token-file cannot both be set")
	}

	if cmdStr != "" {
		return NewCommandTokenSource(cmdStr)
	}
	if fileStr != "" {
		src := NewFileTokenSource(fileStr)
		if _, err := src.Token(ctx); err != nil {
			return nil, err
		}
		return src, nil
	}
	if tok := strings.TrimSpace(os.Getenv(tunnelTokenEnvVar)); tok != "" {
		src := StaticTokenSource(tok)
		if _, err := src.Token(ctx); err != nil {
			return nil, err
		}
		return src, nil
	}

	return nil, fmt.Errorf("%w: pass --token-cmd, pass --token-file, or set %s", ErrNoTunnelCredential, tunnelTokenEnvVar)
}
