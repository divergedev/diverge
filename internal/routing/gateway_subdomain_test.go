package routing

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestGatewayRouter_Reconcile_SubdomainMode(t *testing.T) {
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
				Mode:       "subdomain",
				BaseDomain: "preview.dev.local",
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

	// 1. Verify hostname
	hostnames, _, _ := unstructured.NestedSlice(u.Object, "spec", "hostnames")
	require.Len(t, hostnames, 1)
	assert.Equal(t, "test-env.preview.dev.local", hostnames[0])

	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	require.Len(t, rules, 1)

	// 2. Verify NO header match
	matches, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
	require.Len(t, matches, 1)
	_, hasHeaders, _ := unstructured.NestedSlice(matches[0].(map[string]interface{}), "headers")
	assert.False(t, hasHeaders, "should not have header matches in subdomain mode")

	// 3. Verify NO header removal filter
	_, hasFilters, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "filters")
	assert.False(t, hasFilters, "should not have filters in subdomain mode")
}

func TestGatewayRouter_Reconcile_SubdomainPathPrefix(t *testing.T) {
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
				Mode:       "subdomain",
				BaseDomain: "preview.dev.local",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				PathPrefix: "/api",
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

	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	matches, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
	require.Len(t, matches, 1)

	pathMatch, found, _ := unstructured.NestedMap(matches[0].(map[string]interface{}), "path")
	require.True(t, found)
	assert.Equal(t, "PathPrefix", pathMatch["type"])
	assert.Equal(t, "/api", pathMatch["value"])
}

func TestGatewayRouter_GetExternalURL_Subdomain(t *testing.T) {
	r := &GatewayRouter{}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env"},
		Spec: v1alpha1.EnvironmentSpec{
			Routing: v1alpha1.EnvironmentRouting{
				Mode:       "subdomain",
				BaseDomain: "preview.dev.local",
			},
		},
	}

	url := r.GetExternalURL(env)
	assert.Equal(t, "https://test-env.preview.dev.local", url)
}

func TestGatewayRouter_Reconcile_SubdomainOversizedHostname(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := &GatewayRouter{Client: c, Namespace: "default"}

	longDomain := ""
	for i := 0; i < 250; i++ {
		longDomain += "a"
	}
	longDomain += ".com"

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
				Mode:       "subdomain",
				BaseDomain: longDomain,
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrHostnameTooLong))
}
