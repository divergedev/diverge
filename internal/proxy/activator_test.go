package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestActivatorServer_HealthzAndReadyzDoNotWakeActivator(t *testing.T) {
	var activatorHits atomic.Int32
	activatorBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		activatorHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer activatorBackend.Close()

	kubeClient := fake.NewSimpleClientset()
	cfg := ActivatorProxyConfig{
		ActivatorURL:   activatorBackend.URL,
		TargetSelector: "app=test",
	}

	server, err := NewActivatorServer(context.Background(), cfg, kubeClient)
	require.NoError(t, err)
	defer server.Close()

	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()

		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code, "path %s should return 200", path)
		assert.Equal(t, "ok", rr.Body.String(), "path %s should return ok", path)
	}

	assert.Equal(t, int32(0), activatorHits.Load(), "health/readiness probes must never hit Knative activator")
}

func TestActivatorServer_WakeupStatus(t *testing.T) {
	var activatorHits atomic.Int32
	activatorBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		activatorHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer activatorBackend.Close()

	cfg := ActivatorProxyConfig{
		ActivatorURL:    activatorBackend.URL,
		TargetNamespace: "default",
		TargetSelector:  "app=test",
	}

	t.Run("not ready when scaled to zero", func(t *testing.T) {
		kubeClient := fake.NewSimpleClientset()
		server, err := NewActivatorServer(context.Background(), cfg, kubeClient)
		require.NoError(t, err)
		defer server.Close()

		req := httptest.NewRequest(http.MethodGet, "/-/wakeup-status", nil)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"ready": false}`, rr.Body.String())
		assert.Equal(t, int32(0), activatorHits.Load())
	})

	t.Run("ready when pod is running and ready", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				Labels:    map[string]string{"app": "test"},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				PodIP: "10.0.0.42",
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodReady, Status: corev1.ConditionTrue},
				},
			},
		}
		kubeClient := fake.NewSimpleClientset(pod)
		server, err := NewActivatorServer(context.Background(), cfg, kubeClient)
		require.NoError(t, err)
		defer server.Close()

		time.Sleep(50 * time.Millisecond)

		req := httptest.NewRequest(http.MethodGet, "/-/wakeup-status", nil)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"ready": true}`, rr.Body.String())
		assert.Equal(t, int32(0), activatorHits.Load())
	})
}

func TestActivatorServer_WakeupSplashPage(t *testing.T) {
	wakeCh := make(chan string, 4)
	releaseWake := make(chan struct{})
	var wakeCount atomic.Int32
	activatorBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wakeCount.Add(1)
		wakeCh <- r.Header.Get("Knative-Serving-Revision")
		<-releaseWake
		w.WriteHeader(http.StatusOK)
	}))
	defer activatorBackend.Close()

	kubeClient := fake.NewSimpleClientset()
	cfg := ActivatorProxyConfig{
		ActivatorURL:    activatorBackend.URL,
		TargetNamespace: "default",
		TargetSelector:  "serving.knative.dev/revision=my-rev-00001",
		PreviewEnvValue: "pr-99",
	}

	server, err := NewActivatorServer(context.Background(), cfg, kubeClient)
	require.NoError(t, err)
	defer server.Close()

	req := httptest.NewRequest(http.MethodGet, "http://pr-99.preview.example.com/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("X-Diverge-Wake-Splash", "true")
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusAccepted, rr.Code)
	assert.Equal(t, "text/html; charset=utf-8", rr.Header().Get("Content-Type"))
	assert.Equal(t, "2", rr.Header().Get("Retry-After"))
	assert.Contains(t, rr.Body.String(), "Waking up preview environment...")
	assert.Contains(t, rr.Body.String(), "/-/wakeup-status")

	select {
	case rev := <-wakeCh:
		assert.Equal(t, "my-rev-00001", rev)
	case <-time.After(2 * time.Second):
		t.Fatal("expected background wake request to reach Knative activator")
	}

	// Second splash request while the first background wake is still in flight must be deduplicated
	req2 := httptest.NewRequest(http.MethodGet, "http://pr-99.preview.example.com/", nil)
	req2.Header.Set("Accept", "text/html,application/xhtml+xml")
	req2.Header.Set("X-Diverge-Wake-Splash", "true")
	rr2 := httptest.NewRecorder()
	server.ServeHTTP(rr2, req2)
	assert.Equal(t, http.StatusAccepted, rr2.Code)

	close(releaseWake)
	assert.Equal(t, int32(1), wakeCount.Load(), "in-flight background wake must be deduplicated")
}

func TestActivatorServer_ConnectionHeaderProtection(t *testing.T) {
	activatorBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "preview-123", r.Header.Get("X-Preview-Env"))
		assert.Equal(t, "rev-123", r.Header.Get("Knative-Serving-Revision"))
		assert.Equal(t, "default", r.Header.Get("Knative-Serving-Namespace"))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer activatorBackend.Close()

	kubeClient := fake.NewSimpleClientset()
	cfg := ActivatorProxyConfig{
		ActivatorURL:    activatorBackend.URL,
		TargetNamespace: "default",
		TargetSelector:  "app=test",
		TargetRevision:  "rev-123",
		PreviewEnvValue: "preview-123",
	}

	server, err := NewActivatorServer(context.Background(), cfg, kubeClient)
	require.NoError(t, err)
	defer server.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Connection", "X-Preview-Env, Knative-Serving-Revision, Knative-Serving-Namespace")
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusAccepted, rr.Code)
}

func TestActivatorServer_Routing(t *testing.T) {
	activatorBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "preview-123", r.Header.Get("X-Preview-Env"))
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("activator"))
	}))
	defer activatorBackend.Close()

	podBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "preview-123", r.Header.Get("X-Preview-Env"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pod"))
	}))
	defer podBackend.Close()

	podURL, _ := url.Parse(podBackend.URL)
	podPort, _ := strconv.Atoi(podURL.Port())

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
			PodIP: podURL.Hostname(),
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	tests := []struct {
		name           string
		pod            *corev1.Pod
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "direct pod routing",
			pod:            pod,
			expectedStatus: http.StatusOK,
			expectedBody:   "pod",
		},
		{
			name:           "activator fallback",
			pod:            nil,
			expectedStatus: http.StatusAccepted,
			expectedBody:   "activator",
		},
		{
			name:           "injected headers",
			pod:            pod,
			expectedStatus: http.StatusOK,
			expectedBody:   "pod",
		},
		{
			name:           "response passthrough",
			pod:            nil,
			expectedStatus: http.StatusAccepted,
			expectedBody:   "activator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var kubeClient *fake.Clientset
			if tt.pod != nil {
				kubeClient = fake.NewSimpleClientset(tt.pod)
			} else {
				kubeClient = fake.NewSimpleClientset()
			}

			cfg := ActivatorProxyConfig{
				ActivatorURL:    activatorBackend.URL,
				TargetNamespace: "default",
				TargetSelector:  "app=test",
				TargetPort:      podPort,
				PreviewEnvValue: "preview-123",
			}

			server, err := NewActivatorServer(context.Background(), cfg, kubeClient)
			require.NoError(t, err)
			defer server.Close()

			// allow informer to sync
			time.Sleep(100 * time.Millisecond)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rr := httptest.NewRecorder()

			server.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)
			assert.Equal(t, tt.expectedBody, rr.Body.String())
		})
	}
}

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

	server, err := NewActivatorServer(context.Background(), cfg, kubeClient)
	require.NoError(t, err)
	defer server.Close()

	// Wait for informer sync
	time.Sleep(100 * time.Millisecond)

	ip := server.getReadyPodIP()
	assert.Equal(t, "10.0.0.1", ip)
}
