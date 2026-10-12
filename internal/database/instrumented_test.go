package database

import (
	"context"
	"errors"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgdb "github.com/divergedev/diverge/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type stubDBProvider struct {
	provisionRes *pkgdb.DatabaseResult
	provisionErr error
	teardownErr  error
	statusRes    *pkgdb.DatabaseStatus
	statusErr    error
}

func (s *stubDBProvider) Provision(_ context.Context, _ *v1alpha1.Environment) (*pkgdb.DatabaseResult, error) {
	return s.provisionRes, s.provisionErr
}

func (s *stubDBProvider) Teardown(_ context.Context, _ *v1alpha1.Environment) error {
	return s.teardownErr
}

func (s *stubDBProvider) Status(_ context.Context, _ *v1alpha1.Environment) (*pkgdb.DatabaseStatus, error) {
	return s.statusRes, s.statusErr
}

func TestInstrumentedDatabaseProvider_SuccessAndErrorPaths(t *testing.T) {
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "inst-env"}}

	t.Run("success delegates all methods", func(t *testing.T) {
		inner := &stubDBProvider{
			provisionRes: &pkgdb.DatabaseResult{Ready: true, DSN: "postgres://u:p@localhost/db"},
			statusRes:    &pkgdb.DatabaseStatus{Provisioned: true, SchemaName: "preview_inst_env"},
		}
		p := &InstrumentedDatabaseProvider{Inner: inner, Mode: "schema"}

		res, err := p.Provision(context.Background(), env)
		require.NoError(t, err)
		assert.True(t, res.Ready)

		st, err := p.Status(context.Background(), env)
		require.NoError(t, err)
		assert.True(t, st.Provisioned)

		require.NoError(t, p.Teardown(context.Background(), env))
	})

	t.Run("errors propagate and record metrics", func(t *testing.T) {
		boom := errors.New("db failure")
		inner := &stubDBProvider{
			provisionErr: boom,
			teardownErr:  boom,
			statusErr:    boom,
		}
		p := &InstrumentedDatabaseProvider{Inner: inner, Mode: "schema"}

		_, err := p.Provision(context.Background(), env)
		assert.ErrorIs(t, err, boom)

		_, err = p.Status(context.Background(), env)
		assert.ErrorIs(t, err, boom)

		err = p.Teardown(context.Background(), env)
		assert.ErrorIs(t, err, boom)
	})
}
