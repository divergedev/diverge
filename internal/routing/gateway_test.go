package routing

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestGatewayRouterReconcile(t *testing.T) {
	router := &GatewayRouter{}
	env := &v1alpha1.Environment{}
	
	err := router.Reconcile(context.Background(), env)
	if err != nil {
		t.Errorf("Expected nil, got %v", err)
	}
}

func TestGatewayRouterTeardown(t *testing.T) {
	router := &GatewayRouter{}
	env := &v1alpha1.Environment{}
	
	err := router.Teardown(context.Background(), env)
	if err != nil {
		t.Errorf("Expected nil, got %v", err)
	}
}
