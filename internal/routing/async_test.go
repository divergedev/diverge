package routing

import (
	"context"
	"errors"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
)

type mockAsyncProvider struct {
	name          string
	reconcileErr  error
	teardownErr   error
	reconciled    bool
	teardownOrder *[]string
}

func (m *mockAsyncProvider) Name() string {
	return m.name
}

func (m *mockAsyncProvider) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	m.reconciled = true
	return m.reconcileErr
}

func (m *mockAsyncProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	if m.teardownOrder != nil {
		*m.teardownOrder = append(*m.teardownOrder, m.name)
	}
	return m.teardownErr
}

func TestAsyncRouter_Reconcile(t *testing.T) {
	err1 := errors.New("err1")
	err2 := errors.New("err2")
	p1 := &mockAsyncProvider{name: "p1", reconcileErr: err1}
	p2 := &mockAsyncProvider{name: "p2", reconcileErr: nil}
	p3 := &mockAsyncProvider{name: "p3", reconcileErr: err2}

	r := &AsyncRouter{Providers: []AsyncProvider{p1, p2, p3}}
	err := r.Reconcile(context.Background(), &v1alpha1.Environment{})

	assert.ErrorContains(t, err, "async provider p1: err1")
	assert.ErrorContains(t, err, "async provider p3: err2")
	assert.True(t, p1.reconciled)
	assert.True(t, p2.reconciled)
	assert.True(t, p3.reconciled)
}

func TestAsyncRouter_Teardown(t *testing.T) {
	var order []string
	p1 := &mockAsyncProvider{name: "p1", teardownOrder: &order, teardownErr: errors.New("err1")}
	p2 := &mockAsyncProvider{name: "p2", teardownOrder: &order, teardownErr: nil}
	p3 := &mockAsyncProvider{name: "p3", teardownOrder: &order, teardownErr: errors.New("err3")}

	r := &AsyncRouter{Providers: []AsyncProvider{p1, p2, p3}}
	err := r.Teardown(context.Background(), &v1alpha1.Environment{})

	assert.ErrorContains(t, err, "err1")
	assert.ErrorContains(t, err, "err3")
	assert.Equal(t, []string{"p3", "p2", "p1"}, order)
}

func TestAsyncRouter_GetExternalURL(t *testing.T) {
	r := &AsyncRouter{}
	assert.Equal(t, "", r.GetExternalURL(&v1alpha1.Environment{}))
}
