package routing

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"hegel.dev/go/hegel"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIstioRouterPropertyTeardown(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		client := fake.NewClientBuilder().Build()
		router := &IstioRouter{Client: client}
		env := &v1alpha1.Environment{}

		err := router.Teardown(context.Background(), env)
		if err != nil {
			t.Errorf("Teardown should never error, got: %v", err)
		}
	})
}
