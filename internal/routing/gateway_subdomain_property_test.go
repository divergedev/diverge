package routing

import (
	"context"
	"strings"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"pgregory.net/rapid"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Property: for any valid env name and base domain, subdomain routing
// always produces a valid hostname in the HTTPRoute
func TestGatewaySubdomainProperty_HostnameFormat(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate valid k8s names (lowercase, alphanumeric, dashes, max 63 chars)
		envName := rapid.StringMatching(`^[a-z][a-z0-9-]{0,20}[a-z0-9]$`).Draw(t, "envName")
		baseDomain := rapid.StringMatching(`^[a-z][a-z0-9-]{0,10}\.[a-z]{2,5}$`).Draw(t, "baseDomain")
		svc := rapid.StringMatching(`^[a-z][a-z0-9-]{0,10}[a-z0-9]$`).Draw(t, "svc")

		c := fake.NewClientBuilder().Build()
		r := &GatewayRouter{Client: c, Namespace: "default"}

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: envName, Namespace: "default"},
			Spec: v1alpha1.EnvironmentSpec{
				Deploy: v1alpha1.EnvironmentDeploy{ChangedServices: []string{svc}},
				Routing: v1alpha1.EnvironmentRouting{
					Mode:       "subdomain",
					BaseDomain: baseDomain,
				},
			},
		}

		err := r.Reconcile(context.Background(), env)
		require.NoError(t, err)

		// Fetch the created route
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("HTTPRoute")
		routeName := envName + "-" + svc
		err = c.Get(context.Background(), client.ObjectKey{Name: routeName, Namespace: "default"}, u)
		require.NoError(t, err)

		// Property 1: hostnames is set and contains exactly one entry
		hostnames, found, _ := unstructured.NestedSlice(u.Object, "spec", "hostnames")
		require.True(t, found, "hostnames must be set in subdomain mode")
		require.Len(t, hostnames, 1)

		// Property 2: hostname has the format <envName>.<baseDomain>
		hostname := hostnames[0].(string)
		require.Equal(t, envName+"."+baseDomain, hostname)

		// Property 3: hostname does not contain uppercase or spaces
		require.Equal(t, strings.ToLower(hostname), hostname)
		require.NotContains(t, hostname, " ")

		// Property 4: no header matches in subdomain mode
		rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
		require.Len(t, rules, 1)
		matches, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
		require.Len(t, matches, 1)
		_, hasHeaders, _ := unstructured.NestedSlice(matches[0].(map[string]interface{}), "headers")
		require.False(t, hasHeaders, "subdomain mode must not have header matches")

		// Property 5: no filters in subdomain mode
		_, hasFilters, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "filters")
		require.False(t, hasFilters, "subdomain mode must not have filters")
	})
}

// Property: header mode is unchanged (regression test)
func TestGatewayHeaderModeProperty_StillWorks(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		envName := rapid.StringMatching(`^[a-z][a-z0-9-]{0,20}[a-z0-9]$`).Draw(t, "envName")
		svc := rapid.StringMatching(`^[a-z][a-z0-9-]{0,10}[a-z0-9]$`).Draw(t, "svc")
		headerVal := rapid.StringMatching(`^[a-zA-Z0-9_-]{1,30}$`).Draw(t, "headerVal")

		c := fake.NewClientBuilder().Build()
		r := &GatewayRouter{Client: c, Namespace: "default"}

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: envName, Namespace: "default"},
			Spec: v1alpha1.EnvironmentSpec{
				Deploy: v1alpha1.EnvironmentDeploy{ChangedServices: []string{svc}},
				Routing: v1alpha1.EnvironmentRouting{
					Mode:        "header",
					HeaderValue: headerVal,
				},
			},
		}

		err := r.Reconcile(context.Background(), env)
		require.NoError(t, err)

		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("HTTPRoute")
		err = c.Get(context.Background(), client.ObjectKey{Name: envName + "-" + svc, Namespace: "default"}, u)
		require.NoError(t, err)

		// Property: in header mode, hostnames is NOT set
		_, found, _ := unstructured.NestedSlice(u.Object, "spec", "hostnames")
		require.False(t, found, "header mode must not set hostnames")

		// Property: in header mode, header matches ARE set
		rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
		require.Len(t, rules, 1)
		matches, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
		require.Len(t, matches, 1)
		_, hasHeaders, _ := unstructured.NestedSlice(matches[0].(map[string]interface{}), "headers")
		require.True(t, hasHeaders, "header mode must have header matches")
	})
}
