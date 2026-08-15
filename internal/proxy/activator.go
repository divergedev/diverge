package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ActivatorProxyConfig holds configuration for the Activator proxy.
type ActivatorProxyConfig struct {
	Port            int
	ActivatorURL    string
	TargetNamespace string
	TargetSelector  string
	TargetPort      int
	TargetRevision  string
}

// ActivatorServer is the reverse proxy server for Knative scale-to-zero.
type ActivatorServer struct {
	config         ActivatorProxyConfig
	kubeClient     kubernetes.Interface
	activatorProxy *httputil.ReverseProxy
}

// NewActivatorServer creates a new ActivatorServer.
func NewActivatorServer(cfg ActivatorProxyConfig, kubeClient kubernetes.Interface) (*ActivatorServer, error) {
	activatorURL, err := url.Parse(cfg.ActivatorURL)
	if err != nil {
		return nil, fmt.Errorf("invalid activator URL: %w", err)
	}

	// We use NewSingleHostReverseProxy which keeps the original Host header intact.
	activatorProxy := httputil.NewSingleHostReverseProxy(activatorURL)

	return &ActivatorServer{
		config:         cfg,
		kubeClient:     kubeClient,
		activatorProxy: activatorProxy,
	}, nil
}

// ServeHTTP handles incoming requests.
func (s *ActivatorServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	readyPodIP := s.getReadyPodIP(r.Context())
	if readyPodIP != "" {
		targetURL, _ := url.Parse(fmt.Sprintf("http://%s:%d", readyPodIP, s.config.TargetPort))
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

func (s *ActivatorServer) getReadyPodIP(ctx context.Context) string {
	pods, err := s.kubeClient.CoreV1().Pods(s.config.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: s.config.TargetSelector,
	})
	if err != nil {
		return ""
	}

	for _, pod := range pods.Items {
		if isPodReady(&pod) && pod.Status.PodIP != "" {
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
