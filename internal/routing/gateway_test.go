package routing

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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

		// Verify edge header stripping filter
		filters, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "filters")
		require.Len(t, filters, 1)
		assert.Equal(t, "RequestHeaderModifier", filters[0].(map[string]interface{})["type"])
		headerModifier, _, _ := unstructured.NestedMap(filters[0].(map[string]interface{}), "requestHeaderModifier")
		removes, _, _ := unstructured.NestedSlice(headerModifier, "remove")
		assert.Contains(t, removes, "x-custom-env")
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

func TestGatewayRouter_Reconcile_Errors(t *testing.T) {
	c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			return errors.New("get error")
		},
	}).Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"web"},
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get error")
}

func TestGatewayRouter_Teardown_Errors(t *testing.T) {
	c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			return errors.New("list error")
		},
	}).Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
	}

	err := r.Teardown(context.Background(), env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list error")
}

func TestGatewayRouter_Reconcile_PathPrefix(t *testing.T) {
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
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				PathPrefix: "/api/test",
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1")
	u.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web", Namespace: "default"}, u)
	require.NoError(t, err)

	rules, found, err := unstructured.NestedSlice(u.Object, "spec", "rules")
	require.NoError(t, err)
	require.True(t, found, "spec.rules should exist")
	require.NotEmpty(t, rules)

	rule, ok := rules[0].(map[string]interface{})
	require.True(t, ok, "rule should be a map")
	matches, found, err := unstructured.NestedSlice(rule, "matches")
	require.NoError(t, err)
	require.True(t, found, "matches should exist")
	require.NotEmpty(t, matches)

	match, ok := matches[0].(map[string]interface{})
	require.True(t, ok, "match should be a map")

	// Verify path match
	pathMatch, found, err := unstructured.NestedMap(match, "path")
	require.NoError(t, err)
	require.True(t, found, "path match should exist")
	assert.Equal(t, "/api/test", pathMatch["value"])
	assert.Equal(t, "PathPrefix", pathMatch["type"])
}

func TestGatewayRouter_Reconcile_MeshRoutes(t *testing.T) {
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
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "web-svc",
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	// Verify mesh HTTPRoute was created and has no filters
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1")
	u.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web-mesh", Namespace: "default"}, u)
	require.NoError(t, err)

	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	require.Len(t, rules, 1)

	_, found, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "filters")
	assert.False(t, found, "mesh route should not have filters")
}

func TestGatewayRouter_Reconcile_StickyCookie(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	tests := []struct {
		name        string
		envName     string
		headerKey   string
		headerValue string
		protocol    string
		mode        string
		cookieSpec  *v1alpha1.CookieSpec
		wantCookie  bool
		wantRegex   string
		wantSetC    string
	}{
		{
			name:        "Cookie enabled with defaults",
			envName:     "feat-1",
			headerKey:   "x-env",
			headerValue: "feat-1",
			cookieSpec:  &v1alpha1.CookieSpec{Enabled: true},
			wantCookie:  true,
			wantRegex:   `(?:^|;\s*)x-env=feat-1(?:;|$)`,
			wantSetC:    `x-env=feat-1; Path=/; Max-Age=86400; SameSite=Lax`,
		},
		{
			name:        "Regex injection escaped",
			envName:     "feat.1",
			headerKey:   "x-env",
			headerValue: "feat.1",
			cookieSpec:  &v1alpha1.CookieSpec{Enabled: true},
			wantCookie:  true,
			wantRegex:   `(?:^|;\s*)x-env=feat\.1(?:;|$)`,
			wantSetC:    `x-env=feat.1; Path=/; Max-Age=86400; SameSite=Lax`,
		},
		{
			name:        "SameSite None includes Secure",
			envName:     "feat-1",
			headerKey:   "x-env",
			headerValue: "feat-1",
			cookieSpec:  &v1alpha1.CookieSpec{Enabled: true, SameSite: "None"},
			wantCookie:  true,
			wantRegex:   `(?:^|;\s*)x-env=feat-1(?:;|$)`,
			wantSetC:    `x-env=feat-1; Path=/; Max-Age=86400; SameSite=None; Secure`,
		},
		{
			name:        "Explicit Secure flag with Lax",
			envName:     "feat-1",
			headerKey:   "x-env",
			headerValue: "feat-1",
			cookieSpec:  &v1alpha1.CookieSpec{Enabled: true, SameSite: "Lax", Secure: true},
			wantCookie:  true,
			wantRegex:   `(?:^|;\s*)x-env=feat-1(?:;|$)`,
			wantSetC:    `x-env=feat-1; Path=/; Max-Age=86400; SameSite=Lax; Secure`,
		},
		{
			name:        "Custom MaxAge",
			envName:     "feat-1",
			headerKey:   "x-env",
			headerValue: "feat-1",
			cookieSpec:  &v1alpha1.CookieSpec{Enabled: true, MaxAge: 3600},
			wantCookie:  true,
			wantRegex:   `(?:^|;\s*)x-env=feat-1(?:;|$)`,
			wantSetC:    `x-env=feat-1; Path=/; Max-Age=3600; SameSite=Lax`,
		},
		{
			name:        "Subdomain mode prevents cookie match and Set-Cookie",
			envName:     "feat-1",
			headerKey:   "x-env",
			headerValue: "feat-1",
			mode:        "subdomain",
			cookieSpec:  &v1alpha1.CookieSpec{Enabled: true},
			wantCookie:  false,
		},
		{
			name:        "GRPCRoute prevents Set-Cookie",
			envName:     "feat-1",
			headerKey:   "x-env",
			headerValue: "feat-1",
			protocol:    "grpc",
			cookieSpec:  &v1alpha1.CookieSpec{Enabled: true},
			wantCookie:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: tt.envName, Namespace: "default"},
				Spec: v1alpha1.EnvironmentSpec{
					Deploy: v1alpha1.EnvironmentDeploy{
						ChangedServices: []string{"web"},
					},
					Routing: v1alpha1.EnvironmentRouting{
						HeaderKey:   tt.headerKey,
						HeaderValue: tt.headerValue,
						Cookie:      tt.cookieSpec,
						Mode:        tt.mode,
						BaseDomain:  "example.com",
					},
					ServiceConfig: &v1alpha1.ServicePreviewConfig{
						Protocol: tt.protocol,
					},
				},
			}

			err := r.Reconcile(context.Background(), env)
			require.NoError(t, err)

			kind := "HTTPRoute"
			apiVersion := "gateway.networking.k8s.io/v1"
			if tt.protocol == "grpc" {
				kind = "GRPCRoute"
				apiVersion = "gateway.networking.k8s.io/v1alpha2"
			}

			u := &unstructured.Unstructured{}
			u.SetAPIVersion(apiVersion)
			u.SetKind(kind)
			err = c.Get(context.Background(), client.ObjectKey{Name: tt.envName + "-web", Namespace: "default"}, u)
			require.NoError(t, err)

			rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
			require.Len(t, rules, 1)

			matches, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
			if tt.wantCookie {
				require.Len(t, matches, 2, "expected header match and cookie match")

				// header precedence: header match first
				headers0, _, _ := unstructured.NestedSlice(matches[0].(map[string]interface{}), "headers")
				assert.Equal(t, "Exact", headers0[0].(map[string]interface{})["type"])
				assert.Equal(t, tt.headerKey, headers0[0].(map[string]interface{})["name"])

				// cookie match second
				headers1, _, _ := unstructured.NestedSlice(matches[1].(map[string]interface{}), "headers")
				assert.Equal(t, "RegularExpression", headers1[0].(map[string]interface{})["type"])
				assert.Equal(t, "Cookie", headers1[0].(map[string]interface{})["name"])
				assert.Equal(t, tt.wantRegex, headers1[0].(map[string]interface{})["value"])
			} else {
				require.Len(t, matches, 1, "expected only 1 match (either header or hostname)")
			}

			filters, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "filters")
			hasSetCookie := false
			for _, f := range filters {
				fMap := f.(map[string]interface{})
				if fMap["type"] == "ResponseHeaderModifier" {
					rm, _, _ := unstructured.NestedMap(fMap, "responseHeaderModifier")
					add, _, _ := unstructured.NestedSlice(rm, "add")
					for _, h := range add {
						hMap := h.(map[string]interface{})
						if hMap["name"] == "Set-Cookie" {
							hasSetCookie = true
							assert.Equal(t, tt.wantSetC, hMap["value"])
						}
					}
				}
			}

			if tt.wantCookie {
				assert.True(t, hasSetCookie, "expected Set-Cookie filter")
			} else {
				assert.False(t, hasSetCookie, "did not expect Set-Cookie filter")
			}
		})
	}
}

func TestGatewayRouter_Reconcile_CookieRegexDoesNotMatchOverlapping(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "feat-1", Namespace: "default"},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"web"},
			},
			Routing: v1alpha1.EnvironmentRouting{
				HeaderKey:   "x-env",
				HeaderValue: "feat-1",
				Cookie:      &v1alpha1.CookieSpec{Enabled: true},
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1")
	u.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "feat-1-web", Namespace: "default"}, u)
	require.NoError(t, err)

	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	matches, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
	headers, _, _ := unstructured.NestedSlice(matches[1].(map[string]interface{}), "headers")
	pattern := headers[0].(map[string]interface{})["value"].(string)

	importRegexp := regexp.MustCompile(pattern)

	assert.True(t, importRegexp.MatchString("x-env=feat-1"))
	assert.True(t, importRegexp.MatchString("x-env=feat-1; Other=cookie"))
	assert.True(t, importRegexp.MatchString("Other=cookie; x-env=feat-1"))
	assert.False(t, importRegexp.MatchString("x-env=feat-10"), "feat-1 should not match feat-10")
	assert.False(t, importRegexp.MatchString("my-x-env=feat-1"), "should match whole key")
}

func TestGatewayRouter_Reconcile_WebSocket(t *testing.T) {
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
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				WebSocket: &v1alpha1.WebSocketSpec{
					Enabled: true,
				},
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1")
	u.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web", Namespace: "default"}, u)
	require.NoError(t, err)

	rules, found, err := unstructured.NestedSlice(u.Object, "spec", "rules")
	require.NoError(t, err)
	require.True(t, found, "spec.rules should exist")
	require.Len(t, rules, 1)

	rule := rules[0].(map[string]interface{})
	timeouts, found, err := unstructured.NestedMap(rule, "timeouts")
	require.NoError(t, err)
	require.True(t, found, "timeouts should exist")
	assert.Equal(t, "0s", timeouts["request"])
}

func TestGatewayRouter_Reconcile_WebSocketWithPath(t *testing.T) {
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
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				WebSocket: &v1alpha1.WebSocketSpec{
					Enabled: true,
					Path:    "/ws",
					Timeout: "3600s",
				},
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1")
	u.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web", Namespace: "default"}, u)
	require.NoError(t, err)

	rules, found, err := unstructured.NestedSlice(u.Object, "spec", "rules")
	require.NoError(t, err)
	require.True(t, found, "spec.rules should exist")
	require.Len(t, rules, 2) // One for /ws, one for everything else

	// Check WS rule
	wsRule := rules[0].(map[string]interface{})
	timeouts, found, err := unstructured.NestedMap(wsRule, "timeouts")
	require.NoError(t, err)
	require.True(t, found, "timeouts should exist on WS rule")
	assert.Equal(t, "3600s", timeouts["request"])

	wsMatches, _, _ := unstructured.NestedSlice(wsRule, "matches")
	wsMatch := wsMatches[0].(map[string]interface{})
	pathMatch, _, _ := unstructured.NestedMap(wsMatch, "path")
	assert.Equal(t, "/ws", pathMatch["value"])

	// Check default rule
	defaultRule := rules[1].(map[string]interface{})
	_, defaultHasTimeouts, _ := unstructured.NestedMap(defaultRule, "timeouts")
	assert.False(t, defaultHasTimeouts, "default rule should not have timeouts")
}

func TestGatewayRouter_Reconcile_WebSocketPathPrefixComposition(t *testing.T) {
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
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				PathPrefix: "/api/v1",
				WebSocket: &v1alpha1.WebSocketSpec{
					Enabled: true,
					Path:    "/ws",
				},
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1")
	u.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web", Namespace: "default"}, u)
	require.NoError(t, err)

	rules, found, err := unstructured.NestedSlice(u.Object, "spec", "rules")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, rules, 2)

	wsRule := rules[0].(map[string]interface{})
	wsMatches, _, _ := unstructured.NestedSlice(wsRule, "matches")
	wsMatch := wsMatches[0].(map[string]interface{})
	pathMatch, _, _ := unstructured.NestedMap(wsMatch, "path")
	assert.Equal(t, "/api/v1/ws", pathMatch["value"])
}

func TestGatewayRouter_Reconcile_WebSocketScoping(t *testing.T) {
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
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "api",
				WebSocket: &v1alpha1.WebSocketSpec{
					Enabled: true,
					Path:    "/ws",
				},
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.NoError(t, err)

	// API service should have WS rule (2 rules)
	uAPI := &unstructured.Unstructured{}
	uAPI.SetAPIVersion("gateway.networking.k8s.io/v1")
	uAPI.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-api", Namespace: "default"}, uAPI)
	require.NoError(t, err)
	rulesAPI, _, _ := unstructured.NestedSlice(uAPI.Object, "spec", "rules")
	assert.Len(t, rulesAPI, 2)

	// Web service should NOT have WS rule (1 rule)
	uWeb := &unstructured.Unstructured{}
	uWeb.SetAPIVersion("gateway.networking.k8s.io/v1")
	uWeb.SetKind("HTTPRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-web", Namespace: "default"}, uWeb)
	require.NoError(t, err)
	rulesWeb, _, _ := unstructured.NestedSlice(uWeb.Object, "spec", "rules")
	assert.Len(t, rulesWeb, 1)
}
