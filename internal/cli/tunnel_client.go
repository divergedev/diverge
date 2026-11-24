package cli

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/divergedev/diverge/internal/tunnelclient"
)

// TunnelClient wraps tunnelclient.Client for backwards compatibility with diverge dev
// and existing unit tests while sharing the Protocol v3/v2 client implementation.
type TunnelClient struct {
	inner            *tunnelclient.Client
	httpClient       *http.Client
	tunnelHTTPClient *http.Client
	Ready            chan struct{}
}

func isLoopbackHost(host string) bool {
	return tunnelclient.IsLoopbackHost(host)
}

func tunnelBaseTransport(serverAddr string) http.RoundTripper {
	return tunnelclient.BaseTransport(serverAddr)
}

// NewTunnelClientWithTokenSource creates a TunnelClient using a dynamic TokenSource and optional
// base transport (defaults to http.DefaultTransport).
func NewTunnelClientWithTokenSource(
	serverAddr string,
	localPort int,
	previewID, service, namespace string,
	tokenSource TokenSource,
	baseTransport http.RoundTripper,
	logger *slog.Logger,
) *TunnelClient {
	if logger == nil {
		logger = slog.Default()
	}
	client, err := tunnelclient.New(tunnelclient.Config{
		ServerAddr:       serverAddr,
		LocalHost:        "127.0.0.1",
		LocalPort:        localPort,
		PreviewID:        previewID,
		Service:          service,
		Namespace:        namespace,
		ClientInstanceID: tunnelclient.LoadOrCreateClientInstanceID(),
		TokenSource:      tokenSource,
		BaseTransport:    baseTransport,
		Logger:           logger,
	})
	if err != nil {
		logger.Error("invalid tunnel client configuration", "err", err)
		return &TunnelClient{Ready: make(chan struct{})}
	}

	return &TunnelClient{
		inner:            client,
		tunnelHTTPClient: client.TunnelHTTPClient(),
		Ready:            client.Ready,
	}
}

// NewTunnelClient is a backward-compatible wrapper creating a TunnelClient with a static token.
func NewTunnelClient(serverAddr string, localPort int, previewID, service, namespace, token string, logger *slog.Logger) *TunnelClient {
	return NewTunnelClientWithTokenSource(serverAddr, localPort, previewID, service, namespace, StaticTokenSource(token), tunnelBaseTransport(serverAddr), logger)
}

func (tc *TunnelClient) syncOverrides() {
	if tc.inner == nil {
		return
	}
	if tc.httpClient != nil {
		tc.inner.SetLocalHTTPClient(tc.httpClient)
	}
	if tc.tunnelHTTPClient != nil {
		tc.inner.SetTunnelHTTPClient(tc.tunnelHTTPClient)
	}
}

// ReadyInfo returns the most recent TunnelReady payload received from the server.
func (tc *TunnelClient) ReadyInfo() tunnelclient.ReadyInfo {
	if tc.inner == nil {
		return tunnelclient.ReadyInfo{}
	}
	return tc.inner.ReadyInfo()
}

// ConnectWithRetry establishes and maintains the tunnel until ctx cancels or a fatal error occurs.
func (tc *TunnelClient) ConnectWithRetry(ctx context.Context) error {
	if tc.inner == nil {
		return context.Canceled
	}
	tc.syncOverrides()
	return tc.inner.ConnectWithRetry(ctx)
}

// Connect performs one tunnel stream lifecycle.
func (tc *TunnelClient) Connect(ctx context.Context) error {
	if tc.inner == nil {
		return context.Canceled
	}
	tc.syncOverrides()
	return tc.inner.Connect(ctx)
}
