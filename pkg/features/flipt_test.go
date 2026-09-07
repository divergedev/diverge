package features

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type mockFliptServer struct {
	mu          sync.Mutex
	namespaces  map[string]map[string]interface{}
	flags       map[string]map[string]map[string]interface{} // ns -> flagKey -> flag
	variants    map[string]map[string]map[string]string      // ns -> flagKey -> variant
	requireAuth string
}

func newMockFliptServer(authHeader string) (*httptest.Server, *mockFliptServer) {
	mock := &mockFliptServer{
		namespaces:  make(map[string]map[string]interface{}),
		flags:       make(map[string]map[string]map[string]interface{}),
		variants:    make(map[string]map[string]map[string]string),
		requireAuth: authHeader,
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		defer mock.mu.Unlock()

		if mock.requireAuth != "" {
			if r.Header.Get("Authorization") != mock.requireAuth {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		}

		path := r.URL.Path

		// Namespaces CRUD
		if path == "/api/v1/namespaces" && r.Method == http.MethodPost {
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			key := body["key"].(string)
			if _, exists := mock.namespaces[key]; exists {
				http.Error(w, `{"error":"namespace already exists"}`, http.StatusConflict)
				return
			}
			mock.namespaces[key] = body
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(body)
			return
		}

		if strings.HasPrefix(path, "/api/v1/namespaces/") && !strings.Contains(path, "/flags") {
			nsKey := strings.TrimPrefix(path, "/api/v1/namespaces/")
			if r.Method == http.MethodGet {
				if ns, exists := mock.namespaces[nsKey]; exists {
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(ns)
					return
				}
				http.Error(w, `{"error":"namespace not found"}`, http.StatusNotFound)
				return
			}
			if r.Method == http.MethodDelete {
				if _, exists := mock.namespaces[nsKey]; exists {
					delete(mock.namespaces, nsKey)
					delete(mock.flags, nsKey)
					delete(mock.variants, nsKey)
					w.WriteHeader(http.StatusOK)
					return
				}
				http.Error(w, `{"error":"namespace not found"}`, http.StatusNotFound)
				return
			}
		}

		// Flags CRUD: /api/v1/namespaces/{ns}/flags...
		if strings.Contains(path, "/flags") {
			parts := strings.Split(strings.TrimPrefix(path, "/api/v1/namespaces/"), "/")
			nsKey := parts[0]

			// Variant check: /api/v1/namespaces/{ns}/flags/{flag}/variants
			if len(parts) >= 4 && parts[1] == "flags" && parts[3] == "variants" {
				flagKey := parts[2]
				if r.Method == http.MethodPost || r.Method == http.MethodPut {
					var varBody map[string]string
					_ = json.NewDecoder(r.Body).Decode(&varBody)
					if mock.variants[nsKey] == nil {
						mock.variants[nsKey] = make(map[string]map[string]string)
					}
					mock.variants[nsKey][flagKey] = varBody
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(varBody)
					return
				}
			}

			// Flag creation: /api/v1/namespaces/{ns}/flags
			if len(parts) == 2 && parts[1] == "flags" && r.Method == http.MethodPost {
				var flagBody map[string]interface{}
				_ = json.NewDecoder(r.Body).Decode(&flagBody)
				flagKey := flagBody["key"].(string)
				if mock.flags[nsKey] == nil {
					mock.flags[nsKey] = make(map[string]map[string]interface{})
				}
				mock.flags[nsKey][flagKey] = flagBody
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(flagBody)
				return
			}

			// Flag update: /api/v1/namespaces/{ns}/flags/{flag}
			if len(parts) == 3 && parts[1] == "flags" && r.Method == http.MethodPut {
				flagKey := parts[2]
				var flagBody map[string]interface{}
				_ = json.NewDecoder(r.Body).Decode(&flagBody)
				if mock.flags[nsKey] == nil {
					mock.flags[nsKey] = make(map[string]map[string]interface{})
				}
				mock.flags[nsKey][flagKey] = flagBody
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(flagBody)
				return
			}
		}

		http.NotFound(w, r)
	}))

	return ts, mock
}

func TestFliptProvider_Lifecycle(t *testing.T) {
	ts, mock := newMockFliptServer("Bearer secret-admin-token")
	defer ts.Close()

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flipt-creds",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url":         []byte(ts.URL),
			"adminToken":  []byte("secret-admin-token"),
			"clientToken": []byte("client-read-token"),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	provider := NewFliptProvider(c, scheme, logr.Discard())
	assert.Equal(t, "flipt", provider.Type())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-42",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "flipt",
				ConnectionRef: "flipt-creds",
				Overrides: map[string]string{
					"checkout_v2": "true",
					"dark_mode":   "false",
					"variant_val": "matrix-green",
				},
			},
		},
	}

	ctx := context.Background()

	// 1. Initial status before provisioning: namespace does not exist
	status, err := provider.Status(ctx, env)
	require.NoError(t, err)
	assert.False(t, status.Ready)
	assert.Contains(t, status.Message, "not found")

	// 2. Provision
	res, err := provider.Provision(ctx, env)
	require.NoError(t, err)
	assert.Equal(t, "flipt", res.ProviderType)
	assert.Equal(t, ts.URL, res.EnvVars["FLIPT_URL"])
	assert.Equal(t, "diverge-pr-42", res.EnvVars["FLIPT_NAMESPACE"])
	assert.Equal(t, "client-read-token", res.EnvVars["FLIPT_AUTH_TOKEN"])

	// Verify server received namespace and flags
	mock.mu.Lock()
	assert.Contains(t, mock.namespaces, "diverge-pr-42")
	nsFlags := mock.flags["diverge-pr-42"]
	require.NotNil(t, nsFlags)
	assert.Equal(t, "BOOLEAN_FLAG_TYPE", nsFlags["checkout_v2"]["type"])
	assert.Equal(t, true, nsFlags["checkout_v2"]["enabled"])
	assert.Equal(t, "BOOLEAN_FLAG_TYPE", nsFlags["dark_mode"]["type"])
	assert.Equal(t, false, nsFlags["dark_mode"]["enabled"])
	assert.Equal(t, "VARIANT_FLAG_TYPE", nsFlags["variant_val"]["type"])
	assert.Equal(t, true, nsFlags["variant_val"]["enabled"])
	assert.Equal(t, "matrix-green", mock.variants["diverge-pr-42"]["variant_val"]["name"])
	mock.mu.Unlock()

	// 3. Status should now be ready
	status, err = provider.Status(ctx, env)
	require.NoError(t, err)
	assert.True(t, status.Ready)
	assert.Contains(t, status.Message, "ready")

	// 4. Teardown
	err = provider.Teardown(ctx, env)
	require.NoError(t, err)

	// Verify namespace was deleted
	mock.mu.Lock()
	assert.NotContains(t, mock.namespaces, "diverge-pr-42")
	mock.mu.Unlock()

	// 5. Idempotent Teardown (404 is ignored)
	err = provider.Teardown(ctx, env)
	require.NoError(t, err)
}

func TestFliptProvider_DualTierSecretResolution(t *testing.T) {
	ts, _ := newMockFliptServer("")
	defer ts.Close()

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	// Secret is located in diverge-system, not in default
	secretSystem := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shared-flipt",
			Namespace: "diverge-system",
		},
		Data: map[string][]byte{
			"endpoint": []byte(ts.URL),
			"token":    []byte("sys-token"),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secretSystem).Build()
	provider := NewFliptProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-dual-tier",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "flipt",
				ConnectionRef: "shared-flipt",
			},
		},
	}

	ctx := context.Background()
	res, err := provider.Provision(ctx, env)
	require.NoError(t, err)
	assert.Equal(t, ts.URL, res.EnvVars["FLIPT_URL"])
	assert.Equal(t, "sys-token", res.EnvVars["FLIPT_AUTH_TOKEN"])
	assert.Equal(t, "diverge-pr-dual-tier", res.EnvVars["FLIPT_NAMESPACE"])
}

func TestFliptProvider_SecretNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	provider := NewFliptProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-missing-sec",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "flipt",
				ConnectionRef: "non-existent-secret",
			},
		},
	}

	ctx := context.Background()
	_, err := provider.Provision(ctx, env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in namespace \"default\" or \"diverge-system\"")
}

func TestFliptProvider_InvalidURL_SSRFProtection(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	badSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bad-url-sec",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url": []byte("ftp://malicious-endpoint:21"),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(badSecret).Build()
	provider := NewFliptProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-bad-url",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "flipt",
				ConnectionRef: "bad-url-sec",
			},
		},
	}

	ctx := context.Background()
	_, err := provider.Provision(ctx, env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid flipt url scheme \"ftp\"")
}

func TestFliptProvider_HttpErrorHandling(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server crash", http.StatusInternalServerError)
	}))
	defer ts.Close()

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "err-sec",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"url": []byte(ts.URL),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	provider := NewFliptProvider(c, scheme, logr.Discard())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-err",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Features: &v1alpha1.FeatureSpec{
				Provider:      "flipt",
				ConnectionRef: "err-sec",
				Overrides: map[string]string{
					"f1": "true",
				},
			},
		},
	}

	ctx := context.Background()
	_, err := provider.Provision(ctx, env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create flipt namespace")
}
