package routing

import (
	"context"
	"errors"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRouter struct {
	reconcileErr error
	teardownErr  error
	url          string
	reconciled   bool
	teardown     bool
}

func (m *mockRouter) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	m.reconciled = true
	return m.reconcileErr
}

func (m *mockRouter) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	m.teardown = true
	return m.teardownErr
}

func (m *mockRouter) GetExternalURL(env *v1alpha1.Environment) string {
	return m.url
}

func TestCompositeRouter_Reconcile(t *testing.T) {
	t.Run("all succeed", func(t *testing.T) {
		r1 := &mockRouter{}
		r2 := &mockRouter{}
		c := &CompositeRouter{Routers: map[string]Router{"r1": r1, "r2": r2}}
		err := c.Reconcile(context.Background(), nil)
		assert.NoError(t, err)
		assert.True(t, r1.reconciled)
		assert.True(t, r2.reconciled)
	})

	t.Run("partial failure", func(t *testing.T) {
		r1 := &mockRouter{}
		r2 := &mockRouter{reconcileErr: errors.New("fail2")}
		c := &CompositeRouter{Routers: map[string]Router{"r1": r1, "r2": r2}}
		err := c.Reconcile(context.Background(), nil)
		var pfErr *PartialFailureError
		require.ErrorAs(t, err, &pfErr)
		assert.Contains(t, pfErr.Succeeded, "r1")
		assert.Equal(t, map[string]error{"r2": r2.reconcileErr}, pfErr.Failed)
	})

	t.Run("all fail", func(t *testing.T) {
		r1 := &mockRouter{reconcileErr: errors.New("fail1")}
		r2 := &mockRouter{reconcileErr: errors.New("fail2")}
		c := &CompositeRouter{Routers: map[string]Router{"r1": r1, "r2": r2}}
		err := c.Reconcile(context.Background(), nil)
		assert.ErrorContains(t, err, "fail1")
		assert.ErrorContains(t, err, "fail2")
		var pfErr *PartialFailureError
		assert.False(t, errors.As(err, &pfErr))
	})
}

func TestCompositeRouter_Teardown(t *testing.T) {
	r1 := &mockRouter{teardownErr: errors.New("fail1")}
	r2 := &mockRouter{}
	c := &CompositeRouter{Routers: map[string]Router{"r1": r1, "r2": r2}}
	err := c.Teardown(context.Background(), nil)
	assert.ErrorContains(t, err, "fail1")
	assert.True(t, r1.teardown)
	assert.True(t, r2.teardown)
}

func TestCompositeRouter_GetExternalURL(t *testing.T) {
	r1 := &mockRouter{}
	r2 := &mockRouter{url: "http://test"}
	c := &CompositeRouter{Routers: map[string]Router{"r1": r1, "r2": r2}}
	url := c.GetExternalURL(nil)
	assert.Equal(t, "http://test", url)
}

func TestNewRouter(t *testing.T) {
	t.Run("default empty spec", func(t *testing.T) {
		r, err := NewRouter("", registry.Deps{})
		require.NoError(t, err)
		_, ok := r.(*GatewayRouter)
		assert.True(t, ok, "empty spec should default to GatewayRouter")
	})

	t.Run("single provider", func(t *testing.T) {
		r, err := NewRouter("noop", registry.Deps{})
		require.NoError(t, err)
		_, ok := r.(*NoopRouter)
		assert.True(t, ok, "spec 'noop' should return NoopRouter directly")
	})

	t.Run("comma-separated providers", func(t *testing.T) {
		r, err := NewRouter("gateway,istio", registry.Deps{})
		require.NoError(t, err)
		comp, ok := r.(*CompositeRouter)
		require.True(t, ok, "comma-separated providers should produce CompositeRouter")
		assert.Len(t, comp.Routers, 2)
		_, hasGateway := comp.Routers["gateway"].(*GatewayRouter)
		_, hasIstio := comp.Routers["istio"].(*IstioRouter)
		assert.True(t, hasGateway, "should contain gateway router")
		assert.True(t, hasIstio, "should contain istio router")
	})

	t.Run("comma-separated with whitespace", func(t *testing.T) {
		r, err := NewRouter(" noop , istio ", registry.Deps{})
		require.NoError(t, err)
		comp, ok := r.(*CompositeRouter)
		require.True(t, ok)
		assert.Len(t, comp.Routers, 2)
		_, hasNoop := comp.Routers["noop"].(*NoopRouter)
		_, hasIstio := comp.Routers["istio"].(*IstioRouter)
		assert.True(t, hasNoop)
		assert.True(t, hasIstio)
	})

	t.Run("invalid sub-router returns error", func(t *testing.T) {
		_, err := NewRouter("gateway,nonexistent-router", registry.Deps{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "creating composite sub-router \"nonexistent-router\"")
	})
}
