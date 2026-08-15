package routing

import (
	"context"
	"strings"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Property: for any valid env name and base domain, subdomain routing
// always produces a valid hostname in the HTTPRoute.
func TestGatewaySubdomainProperty_HostnameFormat(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		envName := hegel.Draw(ht, hegel.FromRegex(`^[a-z][a-z0-9-]{0,20}[a-z0-9]$`, true))
		baseDomain := hegel.Draw(ht, hegel.FromRegex(`^[a-z][a-z0-9-]{0,10}\.[a-z]{2,5}$`, true))
		svc := hegel.Draw(ht, hegel.FromRegex(`^[a-z][a-z0-9-]{0,10}[a-z0-9]$`, true))

		c := fake.NewClientBuilder().Build()
		r := &GatewayRouter{Client: c, Namespace: "default"}

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: envName, Namespace: "default"},
			Spec: v1alpha1.EnvironmentSpec{
				Deploy:  v1alpha1.EnvironmentDeploy{ChangedServices: []string{svc}},
				Routing: v1alpha1.EnvironmentRouting{Mode: "subdomain", BaseDomain: baseDomain},
			},
		}

		err := r.Reconcile(context.Background(), env)
		require.NoError(ht, err)

		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("HTTPRoute")
		err = c.Get(context.Background(), client.ObjectKey{Name: envName + "-" + svc, Namespace: "default"}, u)
		require.NoError(ht, err)

		// Property 1: hostnames is set and contains exactly one entry
		hostnames, found, _ := unstructured.NestedSlice(u.Object, "spec", "hostnames")
		require.True(ht, found, "hostnames must be set in subdomain mode")
		require.Len(ht, hostnames, 1)

		// Property 2: hostname has the format <envName>.<baseDomain>
		hostname := hostnames[0].(string)
		require.Equal(ht, envName+"."+baseDomain, hostname)

		// Property 3: hostname does not contain uppercase or spaces
		require.Equal(ht, strings.ToLower(hostname), hostname)
		require.NotContains(ht, hostname, " ")

		// Property 4: no header matches in subdomain mode
		rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
		require.Len(ht, rules, 1)
		matches, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
		require.Len(ht, matches, 1)
		_, hasHeaders, _ := unstructured.NestedSlice(matches[0].(map[string]interface{}), "headers")
		require.False(ht, hasHeaders, "subdomain mode must not have header matches")

		// Property 5: no filters in subdomain mode
		_, hasFilters, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "filters")
		require.False(ht, hasFilters, "subdomain mode must not have filters")
	})
}

// Property: header mode is unchanged (regression test).
func TestGatewayHeaderModeProperty_StillWorks(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		envName := hegel.Draw(ht, hegel.FromRegex(`^[a-z][a-z0-9-]{0,20}[a-z0-9]$`, true))
		svc := hegel.Draw(ht, hegel.FromRegex(`^[a-z][a-z0-9-]{0,10}[a-z0-9]$`, true))
		headerVal := hegel.Draw(ht, hegel.FromRegex(`^[a-zA-Z0-9_-]{1,20}$`, true))

		c := fake.NewClientBuilder().Build()
		r := &GatewayRouter{Client: c, Namespace: "default"}

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: envName, Namespace: "default"},
			Spec: v1alpha1.EnvironmentSpec{
				Deploy:  v1alpha1.EnvironmentDeploy{ChangedServices: []string{svc}},
				Routing: v1alpha1.EnvironmentRouting{Mode: "header", HeaderValue: headerVal},
			},
		}

		err := r.Reconcile(context.Background(), env)
		require.NoError(ht, err)

		u := &unstructured.Unstructured{}
		u.SetAPIVersion("gateway.networking.k8s.io/v1")
		u.SetKind("HTTPRoute")
		err = c.Get(context.Background(), client.ObjectKey{Name: envName + "-" + svc, Namespace: "default"}, u)
		require.NoError(ht, err)

		// Property: in header mode, hostnames is NOT set
		_, found, _ := unstructured.NestedSlice(u.Object, "spec", "hostnames")
		require.False(ht, found, "header mode must not set hostnames")

		// Property: in header mode, header matches ARE set
		rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
		require.Len(ht, rules, 1)
		matches, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
		require.Len(ht, matches, 1)
		_, hasHeaders, _ := unstructured.NestedSlice(matches[0].(map[string]interface{}), "headers")
		require.True(ht, hasHeaders, "header mode must have header matches")
	})
}
