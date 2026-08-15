package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

// ActivatorProxyConfig holds configuration for the Activator proxy.
type ActivatorProxyConfig struct {
	Port            int
	ActivatorURL    string
	TargetNamespace string
	TargetSelector  string
	TargetPort      int
	TargetRevision  string
	PreviewEnvValue string
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
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

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

	// Inject Knative headers so the activator knows which revision to wake.
	if s.config.TargetRevision != "" {
		r.Header.Set("Knative-Serving-Revision", s.config.TargetRevision)
	}
	r.Header.Set("Knative-Serving-Namespace", s.config.TargetNamespace)
	s.activatorProxy.ServeHTTP(w, r)
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
