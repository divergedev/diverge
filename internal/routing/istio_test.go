package routing

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestIstioRouterReconcile(t *testing.T) {
	router := &IstioRouter{}
	env := &v1alpha1.Environment{}
	
	err := router.Reconcile(context.Background(), env)
	if err != nil {
		t.Errorf("Expected nil, got %v", err)
	}
}

func TestIstioRouterTeardown(t *testing.T) {
	router := &IstioRouter{}
	env := &v1alpha1.Environment{}
	
	err := router.Teardown(context.Background(), env)
	if err != nil {
		t.Errorf("Expected nil, got %v", err)
	}
}
