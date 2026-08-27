//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/stretchr/testify/require"
)

// RequestOpts configures a data-plane HTTP request through the gateway.
type RequestOpts struct {
	// GatewayURL is the base URL of the gateway (e.g. http://localhost:8880).
	GatewayURL string
	// Host overrides the HTTP Host header (for subdomain-mode routing).
	Host string
	// Headers are additional HTTP headers to send.
	Headers map[string]string
	// Path is the request path (defaults to "/").
	Path string
	// Method is the HTTP method (defaults to "GET").
	Method string
}

// Response wraps the data-plane response for assertions.
type Response struct {
	StatusCode int
	Body       string
	Headers    http.Header
}

// SendRequest sends an HTTP request through the gateway and returns the response.
func (f *Framework) SendRequest(ctx context.Context, opts RequestOpts) (*Response, error) {
	if opts.Path == "" {
		opts.Path = "/"
	}
	if opts.Method == "" {
		opts.Method = http.MethodGet
	}

	url := fmt.Sprintf("%s%s", opts.GatewayURL, opts.Path)
	req, err := http.NewRequestWithContext(ctx, opts.Method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	if opts.Host != "" {
		req.Host = opts.Host
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		// Don't follow redirects — we want to assert exact status codes.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Body:       string(body),
		Headers:    resp.Header,
	}, nil
}

// WaitForRouteReachable polls until the gateway returns a non-5xx response
// for the given request options. Route programming can take seconds after
// HTTPRoute creation.
func (f *Framework) WaitForRouteReachable(ctx context.Context, opts RequestOpts, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		resp, err := f.SendRequest(timeoutCtx, opts)
		if err == nil && resp.StatusCode < 500 {
			return nil
		}
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("route not reachable after %s", timeout)
		case <-ticker.C:
		}
	}
}

// DeployEchoServer creates a simple echo Deployment + Service in the given
// namespace that responds with the deployment name on port 8080.
func (f *Framework) DeployEchoServer(ctx context.Context, name, namespace string, port int32) {
	t := f.T
	t.Helper()

	// Create Deployment
	dep := echoDeployment(name, namespace, port)
	err := f.Client.Create(ctx, dep)
	require.NoError(t, err, "Failed to create echo deployment %s", name)

	// Create Service
	svc := echoService(name, namespace, port)
	err = f.Client.Create(ctx, svc)
	require.NoError(t, err, "Failed to create echo service %s", name)

	// Wait for deployment to be ready
	require.Eventually(t, func() bool {
		var d appsv1.Deployment
		if err := f.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &d); err != nil {
			return false
		}
		return d.Status.ReadyReplicas > 0
	}, 2*time.Minute, 2*time.Second, "Echo deployment %s not ready", name)
}
