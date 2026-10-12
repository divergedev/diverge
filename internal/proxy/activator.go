package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

const wakeSplashHTML = `<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Waking up preview environment...</title>
	<style>
		body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0; background: #0f172a; color: #f8fafc; }
		.card { text-align: center; padding: 2.5rem; border-radius: 12px; background: #1e293b; box-shadow: 0 10px 25px rgba(0,0,0,0.3); max-width: 420px; }
		.spinner { width: 40px; height: 40px; margin: 0 auto 1.25rem; border: 3px solid #334155; border-top-color: #38bdf8; border-radius: 50%; animation: spin 0.9s linear infinite; }
		@keyframes spin { to { transform: rotate(360deg); } }
		h1 { font-size: 1.25rem; margin: 0 0 0.5rem; }
		p { font-size: 0.9rem; color: #94a3b8; margin: 0; }
	</style>
</head>
<body>
	<div class="card">
		<div class="spinner"></div>
		<h1>Waking up preview environment...</h1>
		<p>Scaling from zero. This page will reload automatically as soon as the service is ready.</p>
	</div>
	<script>
		setInterval(async () => {
			try {
				const res = await fetch('/-/wakeup-status', { cache: 'no-store' });
				if (res.ok) {
					const data = await res.json();
					if (data && data.ready) {
						window.location.reload();
					}
				}
			} catch (_) {}
		}, 1500);
	</script>
</body>
</html>`

// ActivatorProxyConfig holds configuration for the Activator proxy.
type ActivatorProxyConfig struct {
	Port             int
	ActivatorURL     string
	TargetNamespace  string
	TargetSelector   string
	TargetPort       int
	TargetRevision   string
	PreviewEnvValue  string
	EnableWakeSplash bool
}

// ActivatorServer is the reverse proxy server for Knative scale-to-zero.
type ActivatorServer struct {
	config         ActivatorProxyConfig
	kubeClient     kubernetes.Interface
	activatorProxy *httputil.ReverseProxy
	podLister      listerscorev1.PodLister
	informerCancel context.CancelFunc
}

// NewActivatorServer creates a new ActivatorServer.
func NewActivatorServer(ctx context.Context, cfg ActivatorProxyConfig, kubeClient kubernetes.Interface) (*ActivatorServer, error) {
	activatorURL, err := url.Parse(cfg.ActivatorURL)
	if err != nil {
		return nil, fmt.Errorf("invalid activator URL: %w", err)
	}

	_, err = labels.Parse(cfg.TargetSelector)
	if err != nil {
		return nil, fmt.Errorf("invalid target selector: %w", err)
	}

	// We use NewSingleHostReverseProxy which keeps the original Host header intact.
	activatorProxy := httputil.NewSingleHostReverseProxy(activatorURL)

	informerCtx, cancel := context.WithCancel(ctx)
	factory := informers.NewSharedInformerFactoryWithOptions(kubeClient, 0,
		informers.WithNamespace(cfg.TargetNamespace),
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = cfg.TargetSelector
		}),
	)
	podInformer := factory.Core().V1().Pods()
	lister := podInformer.Lister()

	factory.Start(informerCtx.Done())

	syncCtx, syncCancel := context.WithTimeout(informerCtx, 5*time.Second)
	defer syncCancel()
	if !cache.WaitForCacheSync(syncCtx.Done(), podInformer.Informer().HasSynced) {
		cancel()
		return nil, fmt.Errorf("failed to sync pod informer cache")
	}

	return &ActivatorServer{
		config:         cfg,
		kubeClient:     kubeClient,
		activatorProxy: activatorProxy,
		podLister:      lister,
		informerCancel: cancel,
	}, nil
}

// Close stops the internal informer.
func (s *ActivatorServer) Close() {
	if s.informerCancel != nil {
		s.informerCancel()
	}
}

// ServeHTTP handles incoming requests.
func (s *ActivatorServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	if r.URL.Path == "/-/wakeup-status" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if s.getReadyPodIP() != "" {
			_, _ = w.Write([]byte(`{"ready": true}`))
		} else {
			_, _ = w.Write([]byte(`{"ready": false}`))
		}
		return
	}

	sanitizeConnectionHeader(r.Header, "X-Preview-Env", "Knative-Serving-Revision", "Knative-Serving-Namespace")

	if s.config.PreviewEnvValue != "" {
		r.Header.Set("X-Preview-Env", s.config.PreviewEnvValue)
	}

	readyPodIP := s.getReadyPodIP()
	if readyPodIP != "" {
		targetURL, _ := url.Parse(fmt.Sprintf("http://%s", net.JoinHostPort(readyPodIP, strconv.Itoa(s.config.TargetPort))))
		podProxy := httputil.NewSingleHostReverseProxy(targetURL)
		podProxy.ServeHTTP(w, r)
		return
	}

	if (s.config.EnableWakeSplash || r.Header.Get("X-Diverge-Wake-Splash") == "true") &&
		r.Method == http.MethodGet &&
		strings.Contains(r.Header.Get("Accept"), "text/html") {
		go s.triggerBackgroundWake(r.Host)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(wakeSplashHTML))
		return
	}

	// Inject Knative headers so the activator knows which revision to wake.
	if rev := s.resolveTargetRevision(); rev != "" {
		r.Header.Set("Knative-Serving-Revision", rev)
	}
	r.Header.Set("Knative-Serving-Namespace", s.config.TargetNamespace)
	s.activatorProxy.ServeHTTP(w, r)
}

type discardResponseWriter struct {
	header http.Header
}

func (d *discardResponseWriter) Header() http.Header {
	if d.header == nil {
		d.header = make(http.Header)
	}
	return d.header
}

func (d *discardResponseWriter) Write(b []byte) (int, error) {
	return len(b), nil
}

func (d *discardResponseWriter) WriteHeader(int) {}

func (s *ActivatorServer) triggerBackgroundWake(host string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	targetHost := host
	if targetHost == "" {
		targetHost = "localhost"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+targetHost+"/", nil)
	if err != nil {
		return
	}
	req.Host = host
	if s.config.PreviewEnvValue != "" {
		req.Header.Set("X-Preview-Env", s.config.PreviewEnvValue)
	}
	if rev := s.resolveTargetRevision(); rev != "" {
		req.Header.Set("Knative-Serving-Revision", rev)
	}
	req.Header.Set("Knative-Serving-Namespace", s.config.TargetNamespace)
	s.activatorProxy.ServeHTTP(&discardResponseWriter{}, req)
}

func (s *ActivatorServer) resolveTargetRevision() string {
	if s.config.TargetRevision != "" {
		return s.config.TargetRevision
	}

	sel, err := labels.Parse(s.config.TargetSelector)
	if err == nil {
		if pods, err := s.podLister.Pods(s.config.TargetNamespace).List(sel); err == nil {
			for _, pod := range pods {
				if rev := pod.Labels["serving.knative.dev/revision"]; rev != "" {
					return rev
				}
			}
		}
		if rev, found := sel.RequiresExactMatch("serving.knative.dev/revision"); found && rev != "" {
			return rev
		}
	}

	for _, part := range strings.Split(s.config.TargetSelector, ",") {
		part = strings.TrimSpace(part)
		if after, ok := strings.CutPrefix(part, "serving.knative.dev/revision="); ok && after != "" {
			return after
		}
	}

	return ""
}

func sanitizeConnectionHeader(h http.Header, protected ...string) {
	if h == nil || len(protected) == 0 {
		return
	}
	var kept []string
	var foundConnection bool
	for k, vals := range h {
		if !strings.EqualFold(k, "Connection") {
			continue
		}
		foundConnection = true
		for _, v := range vals {
			for _, token := range strings.Split(v, ",") {
				token = strings.TrimSpace(token)
				if token == "" {
					continue
				}
				isProtected := false
				for _, p := range protected {
					if strings.EqualFold(token, p) {
						isProtected = true
						break
					}
				}
				if !isProtected {
					kept = append(kept, token)
				}
			}
		}
		delete(h, k)
	}
	if foundConnection && len(kept) > 0 {
		h.Set("Connection", strings.Join(kept, ", "))
	}
}

func (s *ActivatorServer) getReadyPodIP() string {
	sel, err := labels.Parse(s.config.TargetSelector)
	if err != nil {
		return ""
	}

	pods, err := s.podLister.Pods(s.config.TargetNamespace).List(sel)
	if err != nil {
		return ""
	}

	for _, pod := range pods {
		if isPodReady(pod) && pod.Status.PodIP != "" {
			return pod.Status.PodIP
		}
	}
	return ""
}

func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
