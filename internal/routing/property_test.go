package routing

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"pgregory.net/rapid"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIstioRouterPropertyTeardown(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringMatching(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).Draw(t, "name")
		ns := rapid.StringMatching(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).Draw(t, "namespace")

		client := fake.NewClientBuilder().Build()
		router := &IstioRouter{Client: client}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
		}

		err := router.Teardown(context.Background(), env)
		if err != nil {
			t.Fatalf("Teardown should never error, got: %v", err)
		}
	})
}
