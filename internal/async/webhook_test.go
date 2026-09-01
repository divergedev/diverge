package async

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWebhookResponseLimit(t *testing.T) {
	// Start a test server that returns > 1MB of JSON
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 1MB + 10KB
		_, _ = w.Write([]byte(`{"resolvedTarget":"` + strings.Repeat("A", (1<<20)+10240) + `"}`))
	}))
	defer ts.Close()

	w := NewWebhookProvisioner(ts.URL)
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	route := v1alpha1.AsyncRouteSpec{
		Protocol: "temporal",
		Target:   "my-queue",
	}

	_, err := w.Provision(context.Background(), env, route)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook provision failed")
	// Because of io.LimitReader and missing closing brace, it will fail to decode
}
