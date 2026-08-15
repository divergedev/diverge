package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestActivatorServer_Healthz(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	cfg := ActivatorProxyConfig{
		ActivatorURL: "http://activator.default.svc.cluster.local",
	}

	server, err := NewActivatorServer(cfg, kubeClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	if rr.Body.String() != "ok" {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), "ok")
	}
}

func TestActivatorServer_Routing(t *testing.T) {
	// Dummy activator backend
	activatorBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("activator"))
	}))
	defer activatorBackend.Close()

	// Dummy pod backend
	podBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pod"))
	}))
	defer podBackend.Close()

	// Need to extract the port from podBackend URL
	// We'll mock the getReadyPodIP to return the host
	// For testing, we mock getReadyPodIP by having a fake pod
	// But it's easier to run httptest on a known IP/port, or adjust test logic.
	// Since we parse url from `http://PodIP:TargetPort`, we can inject PodIP = 127.0.0.1 and TargetPort.
}

// Since getReadyPodIP relies on Kubernetes client, we can test it directly:
func TestGetReadyPodIP(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Labels: map[string]string{
				"app": "test",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.1",
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	kubeClient := fake.NewSimpleClientset(pod)

	cfg := ActivatorProxyConfig{
		ActivatorURL:    "http://activator",
		TargetNamespace: "default",
		TargetSelector:  "app=test",
	}

	server, _ := NewActivatorServer(cfg, kubeClient)
	ip := server.getReadyPodIP(context.Background())
	if ip != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %s", ip)
	}
}
