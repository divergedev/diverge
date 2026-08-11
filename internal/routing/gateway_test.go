package routing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestGatewayRouter_Reconcile_CreatesHTTPRoutes(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"web", "api"},
			},
			Routing: v1alpha1.EnvironmentRouting{
				HeaderKey:   "x-custom-env",
				HeaderValue: "custom-val",
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	// Verify both HTTPRoutes were created
	for _, svc := range []string{"web", "api"} {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("HTTPRoute")
		err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-" + svc, Namespace: "default"}, u)
		require.NoError(t, err, "HTTPRoute for %s should exist", svc)

		// Verify header match
		rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
		require.Len(t, rules, 1)

		matches, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
		headers, _, _ := unstructured.NestedSlice(matches[0].(map[string]interface{}), "headers")
		assert.Equal(t, "x-custom-env", headers[0].(map[string]interface{})["name"])
		assert.Equal(t, "custom-val", headers[0].(map[string]interface{})["value"])
		assert.Equal(t, "Exact", headers[0].(map[string]interface{})["type"])

		// Verify backendRefs
		backends, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "backendRefs")
		require.Len(t, backends, 1)
		assert.Equal(t, "test-env-"+svc, backends[0].(map[string]interface{})["name"])
		assert.Equal(t, int64(8080), backends[0].(map[string]interface{})["port"])

		// Verify parentRefs
		parents, _, _ := unstructured.NestedSlice(u.Object, "spec", "parentRefs")
		require.Len(t, parents, 1)
		assert.Equal(t, "diverge-gateway", parents[0].(map[string]interface{})["name"])
	}
}

func TestGatewayRouter_Reconcile_DefaultHeaders(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "feature-123",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"web"},
			},
			// No custom header config — should use defaults
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1")
	u.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "feature-123-web", Namespace: "default"}, u)
	require.NoError(t, err)

	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	matches, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
	headers, _, _ := unstructured.NestedSlice(matches[0].(map[string]interface{}), "headers")
	// Default header key and value
	assert.Equal(t, "x-diverge-env", headers[0].(map[string]interface{})["name"])
	assert.Equal(t, "feature-123", headers[0].(map[string]interface{})["value"])
}

func TestGatewayRouter_Reconcile_UpdatesExistingRoute(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	// Pre-create an HTTPRoute
	existing := &unstructured.Unstructured{}
	existing.SetAPIVersion("gateway.networking.k8s.io/v1")
	existing.SetKind("HTTPRoute")
	existing.SetName("test-env-web")
	existing.SetNamespace("default")
	existing.Object["spec"] = map[string]interface{}{
		"rules": []interface{}{},
	}
	require.NoError(t, c.Create(context.Background(), existing))

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"web"},
			},
			Routing: v1alpha1.EnvironmentRouting{
				HeaderKey: "x-updated",
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	// Verify updated spec
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1")
	u.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web", Namespace: "default"}, u)
	require.NoError(t, err)

	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	require.Len(t, rules, 1, "should have updated rules")
	matches, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
	headers, _, _ := unstructured.NestedSlice(matches[0].(map[string]interface{}), "headers")
	assert.Equal(t, "x-updated", headers[0].(map[string]interface{})["name"])
}

func TestGatewayRouter_Teardown_DeletesHTTPRoutes(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"web", "api"},
			},
		},
	}

	// Pre-create labeled HTTPRoutes (as Reconcile would)
	for _, svc := range []string{"web", "api"} {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("HTTPRoute")
		u.SetName("test-env-" + svc)
		u.SetNamespace("default")
		u.SetLabels(map[string]string{
			"diverge.io/environment": "test-env",
			"diverge.io/managed-by":  "diverge",
		})
		require.NoError(t, c.Create(context.Background(), u))
	}

	err := r.Teardown(context.Background(), env)
	require.NoError(t, err)

	// Verify both deleted
	for _, svc := range []string{"web", "api"} {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("HTTPRoute")
		err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-" + svc, Namespace: "default"}, u)
		assert.Error(t, err, "HTTPRoute for %s should be deleted", svc)
	}
}

func TestGatewayRouter_Teardown_IgnoresNotFound(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"nonexistent"},
			},
		},
	}

	// Should not error when no labeled routes exist
	err := r.Teardown(context.Background(), env)
	require.NoError(t, err)
}

// CR1 regression: Teardown must clean up stale routes from removed services.
// Scenario: services go from [web, api] to [web] — the "api" route must be deleted.
func TestGatewayRouter_Teardown_CleansStaleRoutes(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	// Step 1: Reconcile with [web, api]
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"web", "api"},
			},
		},
	}
	require.NoError(t, r.Reconcile(context.Background(), env))

	// Verify both routes exist
	for _, svc := range []string{"web", "api"} {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("HTTPRoute")
		err := c.Get(context.Background(), client.ObjectKey{Name: "test-env-" + svc, Namespace: "default"}, u)
		require.NoError(t, err, "HTTPRoute for %s should exist after reconcile", svc)
	}

	// Step 2: Teardown with only [web] in ChangedServices.
	// Label-based teardown should still delete BOTH routes.
	env.Spec.Deploy.ChangedServices = []string{"web"}
	require.NoError(t, r.Teardown(context.Background(), env))

	// Verify BOTH routes are deleted (including stale "api")
	for _, svc := range []string{"web", "api"} {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("HTTPRoute")
		err := c.Get(context.Background(), client.ObjectKey{Name: "test-env-" + svc, Namespace: "default"}, u)
		assert.Error(t, err, "HTTPRoute for %s should be deleted by label-based teardown", svc)
	}
}

func TestGatewayRouter_GetExternalURL(t *testing.T) {
	r := &GatewayRouter{}

	tests := []struct {
		name        string
		externalURL string
		envName     string
		want        string
	}{
		{
			name:        "substitutes env placeholder",
			externalURL: "https://{env}.preview.example.com",
			envName:     "feature-123",
			want:        "https://feature-123.preview.example.com",
		},
		{
			name:        "empty external URL",
			externalURL: "",
			envName:     "test",
			want:        "",
		},
		{
			name:        "no placeholder",
			externalURL: "https://preview.example.com",
			envName:     "test",
			want:        "https://preview.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: tt.envName},
				Spec: v1alpha1.EnvironmentSpec{
					Routing: v1alpha1.EnvironmentRouting{
						ExternalURL: tt.externalURL,
					},
				},
			}
			assert.Equal(t, tt.want, r.GetExternalURL(env))
		})
	}
}

func TestGatewayRouter_Reconcile_UsesEnvNamespace(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	// No explicit Namespace — should fall back to env.Namespace
	r := &GatewayRouter{Client: c}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "my-ns",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"web"},
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1")
	u.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web", Namespace: "my-ns"}, u)
	require.NoError(t, err, "should use env.Namespace when GatewayRouter.Namespace is empty")
}

func TestGatewayRouter_Reconcile_ServiceConfigOverrides(t *testing.T) {
	tests := []struct {
		name          string
		serviceConfig *v1alpha1.ServicePreviewConfig
		wantParentRef string
		wantPort      int64
		wantHeaderKey string
	}{
		{
			name:          "defaults without ServiceConfig",
			serviceConfig: nil,
			wantParentRef: "diverge-gateway",
			wantPort:      8080,
			wantHeaderKey: "x-diverge-env",
		},
		{
			name: "custom parentRef",
			serviceConfig: &v1alpha1.ServicePreviewConfig{
				ParentRef: "banking-waypoint",
			},
			wantParentRef: "banking-waypoint",
			wantPort:      8080,
			wantHeaderKey: "x-diverge-env",
		},
		{
			name: "custom port",
			serviceConfig: &v1alpha1.ServicePreviewConfig{
				Port: 9090,
			},
			wantParentRef: "diverge-gateway",
			wantPort:      9090,
			wantHeaderKey: "x-diverge-env",
		},
		{
			name: "custom headerKey",
			serviceConfig: &v1alpha1.ServicePreviewConfig{
				HeaderKey: "x-preview-id",
			},
			wantParentRef: "diverge-gateway",
			wantPort:      8080,
			wantHeaderKey: "x-preview-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().Build()
			r := &GatewayRouter{Client: c, Namespace: "default"}

			env := &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-env",
					Namespace: "default",
				},
				Spec: v1alpha1.EnvironmentSpec{
					Deploy: v1alpha1.EnvironmentDeploy{
						ChangedServices: []string{"web"},
					},
					Routing: v1alpha1.EnvironmentRouting{
						HeaderKey:   "x-diverge-env",
						HeaderValue: "test-env",
					},
					ServiceConfig: tt.serviceConfig,
				},
			}

			err := r.Reconcile(context.Background(), env)
			require.NoError(t, err)

			u := &unstructured.Unstructured{}
			u.SetAPIVersion("gateway.networking.k8s.io/v1")
			u.SetKind("HTTPRoute")
			err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web", Namespace: "default"}, u)
			require.NoError(t, err)

			parents, _, _ := unstructured.NestedSlice(u.Object, "spec", "parentRefs")
			require.Len(t, parents, 1)
			assert.Equal(t, tt.wantParentRef, parents[0].(map[string]interface{})["name"])

			rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
			backends, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "backendRefs")
			require.Len(t, backends, 1)
			assert.Equal(t, tt.wantPort, backends[0].(map[string]interface{})["port"])

			matches, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
			headers, _, _ := unstructured.NestedSlice(matches[0].(map[string]interface{}), "headers")
			assert.Equal(t, tt.wantHeaderKey, headers[0].(map[string]interface{})["name"])
		})
	}
}
