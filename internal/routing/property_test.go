package routing

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"hegel.dev/go/hegel"
)

func TestIstioRouterPropertyTeardown(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		router := &IstioRouter{}
		env := &v1alpha1.Environment{}
		
		err := router.Teardown(context.Background(), env)
		if err != nil {
			t.Errorf("Teardown should never error, got: %v", err)
		}
	})
}
