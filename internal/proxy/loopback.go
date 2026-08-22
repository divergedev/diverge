package proxy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ServiceRoute represents a mapping from a service name to its upstream URL.
type ServiceRoute struct {
	Name string
	URL  string
}

// RouteTable manages thread-safe service routing.
type RouteTable struct {
	mu       sync.RWMutex
	services map[string]*url.URL
}

// NewRouteTable creates a new RouteTable.
func NewRouteTable() *RouteTable {
	return &RouteTable{
		services: make(map[string]*url.URL),
	}
}

// Update replaces the current routes with the provided services.
func (rt *RouteTable) Update(services []ServiceRoute) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.services = make(map[string]*url.URL)
	for _, s := range services {
		if parsed, err := url.Parse(s.URL); err == nil {
			rt.services[s.Name] = parsed
		}
	}
}

// Lookup finds the upstream URL for a given service name.
func (rt *RouteTable) Lookup(name string) (*url.URL, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	u, ok := rt.services[name]
	return u, ok
}

// Available returns a list of available service names.
func (rt *RouteTable) Available() []string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var names []string
	for name := range rt.services {
		names = append(names, name)
	}
	return names
}

// LoopbackProxy implements a local proxy that routes requests to upstream services.
type LoopbackProxy struct {
	server      *http.Server
	routeTable  *RouteTable
	headerKey   string
	headerValue string
	port        int
	addr        string
	listener    net.Listener
	ready       chan struct{}
	logger      *log.Logger
	firstSync   atomic.Bool
}

// NewLoopbackProxy creates a new LoopbackProxy that routes requests to upstream
// services based on the first path segment, injecting the specified routing header.
func NewLoopbackProxy(headerKey, headerValue string, port int) *LoopbackProxy {
	return &LoopbackProxy{
		routeTable:  NewRouteTable(),
		headerKey:   headerKey,
		headerValue: headerValue,
		port:        port,
		ready:       make(chan struct{}),
		logger:      log.New(os.Stdout, "[diverge proxy] ", log.LstdFlags),
	}
}

// Start binds the server to a local port and begins serving requests.
func (p *LoopbackProxy) Start(ctx context.Context) error {
	addr := fmt.Sprintf("127.0.0.1:%d", p.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			newPort := p.port + 1
			if p.port == 0 {
				newPort = 19001
			}
			return fmt.Errorf("port %d in use. Try: diverge dev --proxy-port %d", p.port, newPort)
		}
		return err
	}

	p.listener = listener
	p.addr = fmt.Sprintf("http://%s", listener.Addr().String())

	mux := http.NewServeMux()
	mux.HandleFunc("/-/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	mux.HandleFunc("/-/readyz", func(w http.ResponseWriter, r *http.Request) {
		if p.firstSync.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("Not Ready"))
		}
	})

	mux.HandleFunc("/", p.handleRequest)

	p.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	close(p.ready)

	go func() {
		<-ctx.Done()
		_ = p.Shutdown(context.Background())
	}()

	err = p.server.Serve(p.listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// handleRequest processes incoming requests and routes them to upstream services.
func (p *LoopbackProxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if strings.Contains(r.Header.Get("Content-Type"), "application/grpc") {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte("gRPC proxying is not yet supported. Track: https://github.com/divergedev/diverge/issues/181"))
		return
	}

	pathParts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(pathParts) == 0 || pathParts[0] == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid path"))
		return
	}

	serviceName := pathParts[0]

	upstreamURL, ok := p.routeTable.Lookup(serviceName)
	if !ok {
		w.WriteHeader(http.StatusBadGateway)
		available := strings.Join(p.routeTable.Available(), ", ")
		msg := fmt.Sprintf("service %q not found\navailable services: %s\nrun 'diverge dev --show-routes' to see available services", serviceName, available)
		_, _ = w.Write([]byte(msg))
		return
	}

	remainingPath := ""
	if len(pathParts) > 1 {
		remainingPath = "/" + pathParts[1]
	} else if strings.HasSuffix(r.URL.Path, "/") {
		remainingPath = "/"
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstreamURL)
			pr.Out.URL.Path = singleJoiningSlash(upstreamURL.Path, remainingPath)
			pr.Out.Header.Set(p.headerKey, p.headerValue)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			p.logger.Printf("proxy error for svc=%s: %v", serviceName, err)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("Bad Gateway"))
		},
	}

	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

	proxy.ServeHTTP(rw, r)

	duration := time.Since(start)
	p.logger.Printf("svc=%s status=%d latency=%s", serviceName, rw.statusCode, duration)
}

func singleJoiningSlash(a, b string) string {
	if a == "" || a == "/" {
		return b
	}
	if b == "" || b == "/" {
		return a
	}
	return strings.TrimRight(a, "/") + "/" + strings.TrimLeft(b, "/")
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// Ready returns a channel that is closed when the proxy is listening.
func (p *LoopbackProxy) Ready() <-chan struct{} {
	return p.ready
}

// Addr returns the actual bound address.
func (p *LoopbackProxy) Addr() string {
	return p.addr
}

// UpdateRoutes updates the route table and marks the proxy as ready.
func (p *LoopbackProxy) UpdateRoutes(services []ServiceRoute) {
	p.routeTable.Update(services)
	p.firstSync.Store(true)
	p.logger.Printf("updated route table with %d services", len(services))
}

// Shutdown gracefully shuts down the server.
func (p *LoopbackProxy) Shutdown(ctx context.Context) error {
	if p.server != nil {
		return p.server.Shutdown(ctx)
	}
	return nil
}

// Close cleans up resources without graceful shutdown.
func (p *LoopbackProxy) Close() error {
	if p.listener != nil {
		return p.listener.Close()
	}
	return nil
}
