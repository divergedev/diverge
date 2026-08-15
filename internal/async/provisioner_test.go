package async

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestNoopProvisioner(t *testing.T) {
	p := &NoopProvisioner{}
	assert.Equal(t, "noop", p.Name())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env"},
	}

	route := v1alpha1.AsyncRouteSpec{
		Protocol: "temporal",
		Target:   "payments",
	}

	res, err := p.Provision(context.Background(), env, route)
	require.NoError(t, err)
	assert.Equal(t, "payments-test-env", res.ResolvedTarget)
	assert.Equal(t, "payments-test-env", res.EnvVars["TEMPORAL_TASK_QUEUE"])

	err = p.Teardown(context.Background(), env, route)
	require.NoError(t, err)
}

func TestWebhookProvisioner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req WebhookRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)

		if req.Action == "provision" {
			res := WebhookResponse{
				ResolvedTarget: "webhook-target",
				EnvVars: map[string]string{
					"CUSTOM_VAR": "webhook-target",
				},
			}
			_ = json.NewEncoder(w).Encode(res)
		} else {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(WebhookResponse{})
		}
	}))
	defer server.Close()

	p := NewWebhookProvisioner(server.URL)
	assert.Equal(t, "webhook", p.Name())

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
	}

	route := v1alpha1.AsyncRouteSpec{
		Protocol: "kafka",
		Target:   "events",
	}

	res, err := p.Provision(context.Background(), env, route)
	require.NoError(t, err)
	assert.Equal(t, "webhook-target", res.ResolvedTarget)
	assert.Equal(t, "webhook-target", res.EnvVars["CUSTOM_VAR"])

	err = p.Teardown(context.Background(), env, route)
	require.NoError(t, err)
}

func TestWebhookProvisioner_Defaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := WebhookResponse{
			ResolvedTarget: "webhook-target",
		}
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer server.Close()

	p := NewWebhookProvisioner(server.URL)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
	}

	route := v1alpha1.AsyncRouteSpec{
		Protocol: "temporal",
		Target:   "payments",
	}

	res, err := p.Provision(context.Background(), env, route)
	require.NoError(t, err)
	assert.Equal(t, "webhook-target", res.ResolvedTarget)
	assert.Equal(t, "webhook-target", res.EnvVars["TEMPORAL_TASK_QUEUE"])
}

func TestWebhookProvisioner_Errors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	p := NewWebhookProvisioner(server.URL)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
	}

	route := v1alpha1.AsyncRouteSpec{
		Protocol: "temporal",
		Target:   "payments",
	}

	_, err := p.Provision(context.Background(), env, route)
	require.Error(t, err)

	err = p.Teardown(context.Background(), env, route)
	require.Error(t, err)
}

func TestNoopProvisioner_KafkaProtocol(t *testing.T) {
	p := &NoopProvisioner{}
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "test-env"}}
	route := v1alpha1.AsyncRouteSpec{Protocol: "kafka", Target: "orders"}

	res, err := p.Provision(context.Background(), env, route)
	require.NoError(t, err)
	assert.Equal(t, "orders-test-env", res.ResolvedTarget)
	assert.Equal(t, "orders-test-env", res.EnvVars["KAFKA_CONSUMER_GROUP"])
}

func TestNoopProvisioner_UnknownProtocol(t *testing.T) {
	p := &NoopProvisioner{}
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "test-env"}}
	route := v1alpha1.AsyncRouteSpec{Protocol: "rabbitmq", Target: "queue"}

	res, err := p.Provision(context.Background(), env, route)
	require.NoError(t, err)
	assert.Equal(t, "queue-test-env", res.ResolvedTarget)
	assert.Empty(t, res.EnvVars, "unknown protocol should not inject default env vars")
}

func TestNoopProvisioner_CustomEnvVarMapping(t *testing.T) {
	p := &NoopProvisioner{}
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "test-env"}}
	route := v1alpha1.AsyncRouteSpec{
		Protocol: "temporal",
		Target:   "payments",
		EnvVarMapping: map[string]string{
			"MY_QUEUE": "{{ .ResolvedTarget }}",
		},
	}

	res, err := p.Provision(context.Background(), env, route)
	require.NoError(t, err)
	assert.Equal(t, "payments-test-env", res.EnvVars["MY_QUEUE"])
	_, hasDefault := res.EnvVars["TEMPORAL_TASK_QUEUE"]
	assert.False(t, hasDefault, "custom mapping should not inject default env var")
}

func TestDefaultEnvVarForProtocol(t *testing.T) {
	assert.Equal(t, "TEMPORAL_TASK_QUEUE", v1alpha1.DefaultEnvVarForProtocol("temporal"))
	assert.Equal(t, "KAFKA_CONSUMER_GROUP", v1alpha1.DefaultEnvVarForProtocol("kafka"))
	assert.Equal(t, "", v1alpha1.DefaultEnvVarForProtocol("rabbitmq"))
	assert.Equal(t, "", v1alpha1.DefaultEnvVarForProtocol(""))
}

func TestWebhookProvisioner_BadJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer server.Close()

	p := NewWebhookProvisioner(server.URL)
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"}}
	route := v1alpha1.AsyncRouteSpec{Protocol: "temporal", Target: "q"}

	_, err := p.Provision(context.Background(), env, route)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestWebhookProvisioner_ConnectionRefused(t *testing.T) {
	p := NewWebhookProvisioner("http://localhost:1") // nothing listening
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"}}
	route := v1alpha1.AsyncRouteSpec{Protocol: "temporal", Target: "q"}

	_, err := p.Provision(context.Background(), env, route)
	require.Error(t, err)
}

func TestWebhookProvisioner_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // slow server
	}))
	defer server.Close()

	p := NewWebhookProvisioner(server.URL)
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"}}
	route := v1alpha1.AsyncRouteSpec{Protocol: "temporal", Target: "q"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := p.Provision(ctx, env, route)
	require.Error(t, err)
}

func TestWebhookProvisioner_CustomEnvVarMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := WebhookResponse{ResolvedTarget: "custom-queue"}
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer server.Close()

	p := NewWebhookProvisioner(server.URL)
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"}}
	route := v1alpha1.AsyncRouteSpec{
		Protocol: "temporal",
		Target:   "payments",
		EnvVarMapping: map[string]string{
			"MY_QUEUE": "{{ .ResolvedTarget }}",
		},
	}

	res, err := p.Provision(context.Background(), env, route)
	require.NoError(t, err)
	assert.Equal(t, "custom-queue", res.EnvVars["MY_QUEUE"])
	// Should NOT have the default TEMPORAL_TASK_QUEUE since explicit mapping
	_, hasDefault := res.EnvVars["TEMPORAL_TASK_QUEUE"]
	assert.False(t, hasDefault)
}
