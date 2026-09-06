package controller

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

type mockSQLExecutor struct {
	executedQuery string
	err           error
}

func (m *mockSQLExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	m.executedQuery = query
	return nil, m.err
}

func TestDirectSetupRunner_EmptySQL(t *testing.T) {
	runner := &DirectSetupRunner{}
	env := &divergeiov1alpha1.Environment{}
	err := runner.RunSetupSQL(context.Background(), env, "", "postgres://admin:pass@localhost/db")
	assert.NoError(t, err)
}

func TestDirectSetupRunner_WithExecutor_Success(t *testing.T) {
	mockExec := &mockSQLExecutor{}
	runner := &DirectSetupRunner{Executor: mockExec}
	env := &divergeiov1alpha1.Environment{}
	sqlQuery := "CREATE SCHEMA IF NOT EXISTS preview_test;"

	err := runner.RunSetupSQL(context.Background(), env, sqlQuery, "")
	assert.NoError(t, err)
	assert.Equal(t, sqlQuery, mockExec.executedQuery)
}

func TestDirectSetupRunner_WithExecutor_Error(t *testing.T) {
	mockExec := &mockSQLExecutor{err: errors.New("syntax error")}
	runner := &DirectSetupRunner{Executor: mockExec}
	env := &divergeiov1alpha1.Environment{}

	err := runner.RunSetupSQL(context.Background(), env, "INVALID SQL", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute setup SQL via executor: syntax error")
}

func TestDirectSetupRunner_NoExecutor_EmptyAdminDSN(t *testing.T) {
	runner := &DirectSetupRunner{}
	env := &divergeiov1alpha1.Environment{}

	err := runner.RunSetupSQL(context.Background(), env, "CREATE SCHEMA foo;", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot execute setup SQL: admin DSN is empty and no executor provided")
}

func TestJobSetupRunner_Interface(t *testing.T) {
	scheme := setupSQLTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &EnvironmentReconciler{Client: c}

	runner := &JobSetupRunner{Reconciler: r}
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
	}

	err := runner.RunSetupSQL(context.Background(), env, "CREATE SCHEMA foo;", "postgres://admin:pass@localhost/db")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrHookInProgress)
}
