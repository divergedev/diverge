package database

import (
	"context"
	"database/sql"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockSQLExecutor struct {
	mock.Mock
}

func (m *mockSQLExecutor) ExecContext(ctx context.Context, query string, args ...any) error {
	callArgs := m.Called(ctx, query, args)
	return callArgs.Error(0)
}

func (m *mockSQLExecutor) QueryRowContext(ctx context.Context, query string, args ...any) Row {
	callArgs := m.Called(ctx, query, args)
	return callArgs.Get(0).(Row)
}

func (m *mockSQLExecutor) Close() error {
	return m.Called().Error(0)
}

type mockRow struct {
	mock.Mock
}

func (m *mockRow) Scan(dest ...any) error {
	callArgs := m.Called(dest)
	if val, ok := callArgs.Get(0).(func(...any) error); ok {
		return val(dest...)
	}
	return callArgs.Error(0)
}

func TestSchemaName(t *testing.T) {
	tests := []struct {
		name      string
		envName   string
		namespace string
		wantError bool
	}{
		{"Normal", "feature-123", "default", false},
		{"With Dots", "feature.123", "default", false},
		{"With Hyphens", "feature-123-foo", "default", false},
		{"SQL Injection", "feature; DROP TABLE users;--", "default", false},
		{"SQL Injection Quotes", "feature' OR 1=1", "default", false},
		{"Empty Name", "", "default", true},
		{"Long Name", "this-is-a-very-long-environment-name-that-exceeds-the-limit-by-a-lot", "default", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.envName,
					Namespace: tt.namespace,
				},
			}
			got, err := SchemaName(env)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Regexp(t, "^[a-z][a-z0-9_]{0,62}$", got)
			}
		})
	}
}

func TestSchemaProvider_Provision(t *testing.T) {
	ctx := context.Background()
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Namespace: "create",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().Build()
	mockExec := new(mockSQLExecutor)

	provider := &SchemaProvider{
		Executor: mockExec,
		Client:   fakeClient,
	}

	schemaName, _ := SchemaName(env)
	mockExec.On("ExecContext", ctx, "CREATE SCHEMA IF NOT EXISTS "+schemaName, mock.Anything).Return(nil)

	status, err := provider.Provision(ctx, env)

	require.NoError(t, err)
	require.NotNil(t, status)
	assert.True(t, status.Ready)
	assert.Equal(t, schemaName, status.SchemaName)
	assert.Equal(t, schemaName, status.Message)
	assert.NotEmpty(t, status.ConnectionSecret)

	mockExec.AssertExpectations(t)

	// check secret created
	secret := &corev1.Secret{}
	err = fakeClient.Get(ctx, client.ObjectKey{Name: status.ConnectionSecret, Namespace: env.PreviewNamespace()}, secret)
	require.NoError(t, err)
}

func TestSchemaProvider_Teardown(t *testing.T) {
	ctx := context.Background()
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}

	fakeClient := fake.NewClientBuilder().Build()
	mockExec := new(mockSQLExecutor)

	provider := &SchemaProvider{
		Executor: mockExec,
		Client:   fakeClient,
	}

	schemaName, _ := SchemaName(env)
	mockExec.On("ExecContext", ctx, "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE", mock.Anything).Return(nil)

	err := provider.Teardown(ctx, env)
	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestSchemaProvider_Status(t *testing.T) {
	ctx := context.Background()
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}

	mockExec := new(mockSQLExecutor)
	provider := &SchemaProvider{
		Executor: mockExec,
	}

	schemaName, _ := SchemaName(env)
	mockRowObj := new(mockRow)
	mockExec.On("QueryRowContext", ctx, "SELECT schema_name FROM information_schema.schemata WHERE schema_name = $1", []any{schemaName}).Return(mockRowObj)

	mockRowObj.On("Scan", mock.Anything).Return(func(dest ...any) error {
		*dest[0].(*string) = schemaName
		return nil
	})

	status, err := provider.Status(ctx, env)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.True(t, status.Ready)
	assert.Equal(t, schemaName, status.SchemaName)
}

func TestSchemaProvider_StatusNotFound(t *testing.T) {
	ctx := context.Background()
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}

	mockExec := new(mockSQLExecutor)
	provider := &SchemaProvider{
		Executor: mockExec,
	}

	schemaName, _ := SchemaName(env)
	mockRowObj := new(mockRow)
	mockExec.On("QueryRowContext", ctx, "SELECT schema_name FROM information_schema.schemata WHERE schema_name = $1", []any{schemaName}).Return(mockRowObj)

	mockRowObj.On("Scan", mock.Anything).Return(sql.ErrNoRows)

	status, err := provider.Status(ctx, env)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.False(t, status.Ready)
}
