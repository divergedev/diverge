package routing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestGatewayRouter_Reconcile(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	router := &GatewayRouter{Client: client}

	env := &v1alpha1.Environment{}
	err := router.Reconcile(context.Background(), env)
	assert.NoError(t, err)
}

func TestGatewayRouter_Teardown(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	router := &GatewayRouter{Client: client}

	env := &v1alpha1.Environment{}
	err := router.Teardown(context.Background(), env)
	assert.NoError(t, err)
}
