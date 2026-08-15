package routing

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func genDNS1123(ht *hegel.T) string {
	chars := []string{"a", "b", "0", "1", "-"}
	first := hegel.Draw(ht, hegel.SampledFrom([]string{"a", "b", "0", "1"}))
	length := hegel.Draw(ht, hegel.Integers(0, 8))
	if length == 0 {
		return first
	}
	res := first
	for i := 0; i < length-1; i++ {
		res += hegel.Draw(ht, hegel.SampledFrom(chars))
	}
	res += hegel.Draw(ht, hegel.SampledFrom([]string{"a", "b", "0", "1"}))
	return res
}

func TestIstioRouterPropertyTeardown(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		name := genDNS1123(ht)
		ns := genDNS1123(ht)

		// Seed the fake client with a managed AuthorizationPolicy
		policy := &unstructured.Unstructured{}
		policy.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "security.istio.io",
			Version: "v1",
			Kind:    "AuthorizationPolicy",
		})
		policy.SetName("diverge-" + name)
		policy.SetNamespace(ns)
		policy.SetLabels(map[string]string{
			"diverge.io/managed-by":  "diverge",
			"diverge.io/environment": name,
		})

		client := fake.NewClientBuilder().
			WithObjects(policy).
			Build()
		router := &IstioRouter{Client: client}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
		}

		err := router.Teardown(context.Background(), env)
		require.NoError(ht, err, "Teardown should not error for valid environments")
	})
}
