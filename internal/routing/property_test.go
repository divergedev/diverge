package routing

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"pgregory.net/rapid"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIstioRouterPropertyTeardown(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringMatching(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).Draw(t, "name")
		ns := rapid.StringMatching(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).Draw(t, "namespace")

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
		require.NoError(t, err, "Teardown should not error for valid environments")
	})
}
