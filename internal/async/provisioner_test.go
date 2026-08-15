package async

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
