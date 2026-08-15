package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestActivatorServer_Healthz(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	cfg := ActivatorProxyConfig{
		ActivatorURL:   "http://activator.default.svc.cluster.local",
		TargetSelector: "app=test",
	}

	server, err := NewActivatorServer(context.Background(), cfg, kubeClient)
	require.NoError(t, err)
	defer server.Close()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "ok", rr.Body.String())
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
			name:           "Ready pod exists",
			pod:            pod,
			expectedStatus: http.StatusOK,
			expectedBody:   "pod",
		},
		{
			name:           "No ready pod",
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
