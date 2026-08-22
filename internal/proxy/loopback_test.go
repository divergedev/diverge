package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/quick"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newH2CTestServer(handler http.Handler) *httptest.Server {
	srv := httptest.NewUnstartedServer(handler)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = protocols
	srv.Start()
	return srv
}

func TestLoopbackProxy_RoutesToUpstream(t *testing.T) {
	upstream := newH2CTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-branch", r.Header.Get("x-diverge-env"))
		assert.Equal(t, "/api/items", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer upstream.Close()

	proxy := NewLoopbackProxy("x-diverge-env", "test-branch", 0, ModePath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- proxy.Start(ctx) }()
	<-proxy.Ready()

	proxy.UpdateRoutes([]ServiceRoute{{Name: "cart-service", URL: upstream.URL}})

	resp, err := http.Get(proxy.Addr() + "/cart-service/api/items")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "OK", string(body))
}

func TestLoopbackProxy_ServiceNotFound(t *testing.T) {
	proxy := NewLoopbackProxy("x-diverge-env", "test", 0, ModePath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go proxy.Start(ctx) //nolint:errcheck
	<-proxy.Ready()

	proxy.UpdateRoutes([]ServiceRoute{
		{Name: "auth-service", URL: "http://localhost:9999"},
		{Name: "cart-service", URL: "http://localhost:9998"},
	})

	resp, err := http.Get(proxy.Addr() + "/unknown-service/path")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Contains(t, string(body), `"unknown-service"`)
	assert.Contains(t, string(body), "available services:")
}

func TestLoopbackProxy_RouteTableUpdate(t *testing.T) {
	upstream1 := newH2CTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("v1"))
	}))
	defer upstream1.Close()

	upstream2 := newH2CTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("v2"))
	}))
	defer upstream2.Close()

	proxy := NewLoopbackProxy("x-diverge-env", "test", 0, ModePath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go proxy.Start(ctx) //nolint:errcheck
	<-proxy.Ready()

	// First route
	proxy.UpdateRoutes([]ServiceRoute{{Name: "svc", URL: upstream1.URL}})
	resp, err := http.Get(proxy.Addr() + "/svc/")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close() //nolint:errcheck
	assert.Equal(t, "v1", string(body))

	// Update route
	proxy.UpdateRoutes([]ServiceRoute{{Name: "svc", URL: upstream2.URL}})
	resp, err = http.Get(proxy.Addr() + "/svc/")
	require.NoError(t, err)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close() //nolint:errcheck
	assert.Equal(t, "v2", string(body))
}

func TestLoopbackProxy_HealthCheck(t *testing.T) {
	proxy := NewLoopbackProxy("x-diverge-env", "test", 0, ModePath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go proxy.Start(ctx) //nolint:errcheck
	<-proxy.Ready()

	resp, err := http.Get(proxy.Addr() + "/-/healthz")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestLoopbackProxy_ReadyzBeforeSync(t *testing.T) {
	proxy := NewLoopbackProxy("x-diverge-env", "test", 0, ModePath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go proxy.Start(ctx) //nolint:errcheck
	<-proxy.Ready()

	// Before first sync — should be 503
	resp, err := http.Get(proxy.Addr() + "/-/readyz")
	require.NoError(t, err)
	resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	// After first sync — should be 200
	proxy.UpdateRoutes([]ServiceRoute{})
	resp, err = http.Get(proxy.Addr() + "/-/readyz")
	require.NoError(t, err)
	resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestLoopbackProxy_HeaderPreservation(t *testing.T) {
	upstream := newH2CTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "custom-value", r.Header.Get("X-Custom"))
		assert.Equal(t, "test-branch", r.Header.Get("x-diverge-env"))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxy := NewLoopbackProxy("x-diverge-env", "test-branch", 0, ModePath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go proxy.Start(ctx) //nolint:errcheck
	<-proxy.Ready()
	proxy.UpdateRoutes([]ServiceRoute{{Name: "svc", URL: upstream.URL}})

	req, _ := http.NewRequest("GET", proxy.Addr()+"/svc/path", nil)
	req.Header.Set("X-Custom", "custom-value")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestLoopbackProxy_GRPCRejection(t *testing.T) {
	proxy := NewLoopbackProxy("x-diverge-env", "test", 0, ModePath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go proxy.Start(ctx) //nolint:errcheck
	<-proxy.Ready()
	proxy.UpdateRoutes([]ServiceRoute{{Name: "svc", URL: "http://localhost:1"}})

	req, _ := http.NewRequest("POST", proxy.Addr()+"/svc/Method", nil)
	req.Header.Set("Content-Type", "application/grpc")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	assert.Contains(t, string(body), "Raw gRPC wire protocol is not supported")
}

func TestLoopbackProxy_EmptyPath(t *testing.T) {
	upstream := newH2CTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// When calling /svc with no trailing path, the upstream gets empty or /
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxy := NewLoopbackProxy("x-diverge-env", "test", 0, ModePath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go proxy.Start(ctx) //nolint:errcheck
	<-proxy.Ready()
	proxy.UpdateRoutes([]ServiceRoute{{Name: "svc", URL: upstream.URL}})

	resp, err := http.Get(proxy.Addr() + "/svc")
	require.NoError(t, err)
	resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestLoopbackProxy_InvalidPath(t *testing.T) {
	proxy := NewLoopbackProxy("x-diverge-env", "test", 0, ModePath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go proxy.Start(ctx) //nolint:errcheck
	<-proxy.Ready()

	resp, err := http.Get(proxy.Addr() + "/")
	require.NoError(t, err)
	resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestLoopbackProxy_GracefulShutdown(t *testing.T) {
	proxy := NewLoopbackProxy("x-diverge-env", "test", 0, ModePath)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() { errCh <- proxy.Start(ctx) }()
	<-proxy.Ready()

	cancel()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timed out")
	}
}

// RouteTable unit tests

func TestRouteTable_Update(t *testing.T) {
	rt := NewRouteTable()
	rt.Update([]ServiceRoute{
		{Name: "svc-a", URL: "http://a:8080"},
		{Name: "svc-b", URL: "http://b:8081"},
	})

	u, ok := rt.Lookup("svc-a")
	assert.True(t, ok)
	assert.Equal(t, "a:8080", u.Host)

	u, ok = rt.Lookup("svc-b")
	assert.True(t, ok)
	assert.Equal(t, "b:8081", u.Host)

	_, ok = rt.Lookup("missing")
	assert.False(t, ok)
}

func TestRouteTable_Available(t *testing.T) {
	rt := NewRouteTable()
	rt.Update([]ServiceRoute{
		{Name: "b-svc", URL: "http://b:80"},
		{Name: "a-svc", URL: "http://a:80"},
	})

	available := rt.Available()
	assert.Len(t, available, 2)
	assert.Contains(t, available, "a-svc")
	assert.Contains(t, available, "b-svc")
}

func TestRouteTable_UpdateReplacesAll(t *testing.T) {
	rt := NewRouteTable()
	rt.Update([]ServiceRoute{{Name: "old", URL: "http://old:80"}})
	rt.Update([]ServiceRoute{{Name: "new", URL: "http://new:80"}})

	_, ok := rt.Lookup("old")
	assert.False(t, ok, "old route should be gone after update")

	_, ok = rt.Lookup("new")
	assert.True(t, ok)
}

// PBT: Route table lookup is deterministic
func TestRouteTable_Deterministic(t *testing.T) {
	f := func(names []string) bool {
		routes := make([]ServiceRoute, 0)
		for _, n := range names {
			n = strings.TrimSpace(n)
			if n == "" || strings.ContainsAny(n, " \t\n\r/:@") {
				continue
			}
			// Only use ASCII names to avoid url.Parse failures on unicode hosts
			isASCII := true
			for _, c := range n {
				if c > 127 {
					isASCII = false
					break
				}
			}
			if !isASCII {
				continue
			}
			routes = append(routes, ServiceRoute{Name: n, URL: "http://localhost:80"})
		}
		rt := NewRouteTable()
		rt.Update(routes)

		for _, r := range routes {
			_, ok := rt.Lookup(r.Name)
			if !ok {
				return false
			}
		}
		return true
	}

	err := quick.Check(f, &quick.Config{MaxCount: 100})
	require.NoError(t, err)
}

func TestHostBasedRouting(t *testing.T) {
	upstream := newH2CTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-branch", r.Header.Get("x-diverge-env"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("HOST OK"))
	}))
	defer upstream.Close()

	proxy := NewLoopbackProxy("x-diverge-env", "test-branch", 0, ModeHost)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- proxy.Start(ctx) }()
	<-proxy.Ready()

	proxy.UpdateRoutes([]ServiceRoute{{Name: "test-svc", URL: upstream.URL}})

	req, _ := http.NewRequest("GET", proxy.Addr()+"/some/path", nil)
	req.Host = "test-svc.localhost:" + strings.Split(proxy.Addr(), ":")[2]

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "HOST OK", string(body))
}

func TestHostBasedRouting_NoSubdomain(t *testing.T) {
	proxy := NewLoopbackProxy("x-diverge-env", "test-branch", 0, ModeHost)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go proxy.Start(ctx) //nolint:errcheck
	<-proxy.Ready()

	proxy.UpdateRoutes([]ServiceRoute{{Name: "test-svc", URL: "http://localhost:1234"}})

	req, _ := http.NewRequest("GET", proxy.Addr()+"/some/path", nil)
	// No subdomain in Host
	req.Host = "localhost:" + strings.Split(proxy.Addr(), ":")[2]

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestGRPCContentTypeBlocked(t *testing.T) {
	proxy := NewLoopbackProxy("x-diverge-env", "test-branch", 0, ModePath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go proxy.Start(ctx) //nolint:errcheck
	<-proxy.Ready()

	proxy.UpdateRoutes([]ServiceRoute{{Name: "test-svc", URL: "http://localhost:1234"}})

	req, _ := http.NewRequest("POST", proxy.Addr()+"/test-svc/Method", nil)
	if !strings.HasPrefix(req.URL.String(), "http://") {
		req.URL.Scheme = "http"
		req.URL.Host = proxy.Addr()
	}
	req.Header.Set("Content-Type", "application/grpc")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	assert.Contains(t, string(body), "Raw gRPC wire protocol is not supported")
	assert.Contains(t, string(body), "Connect")
}

func TestFlushSupport(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}
	// Should not panic
	rw.Flush()
}

func TestCheckLocalhostDNS(t *testing.T) {
	// Should not panic, return boolean
	res := CheckLocalhostDNS()
	_ = res
}

func TestModeAccessor(t *testing.T) {
	p1 := NewLoopbackProxy("x-diverge-env", "test-branch", 0, ModeHost)
	assert.Equal(t, ModeHost, p1.Mode())

	p2 := NewLoopbackProxy("x-diverge-env", "test-branch", 0, ModePath)
	assert.Equal(t, ModePath, p2.Mode())
}
