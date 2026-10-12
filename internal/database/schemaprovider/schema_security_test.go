//go:build !no_schema

package schemaprovider

import (
	"context"
	"strings"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSchemaDatabaseProvider_Security_PasswordIdempotencyAndTeardownReset(t *testing.T) {
	mockExec := &recordingExecutor{}
	provider := NewSchemaDatabaseProvider("postgres://admin:supersecret@localhost:5432/appdb")
	provider.Executor = mockExec

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "sec-env-1"},
	}

	res1, err := provider.Provision(context.Background(), env)
	require.NoError(t, err)
	res2, err := provider.Provision(context.Background(), env)
	require.NoError(t, err)

	assert.Equal(t, res1.DSN, res2.DSN, "idempotent Provision must reuse cached role password")
	assert.NotContains(t, res1.DSN, "supersecret", "workload DSN must never expose admin password")
	assert.Contains(t, res1.SetupSQL, "GRANT CREATE ON SCHEMA")
	assert.Contains(t, res1.SetupSQL, "GRANT ALL ON ALL SEQUENCES IN SCHEMA")

	require.NoError(t, provider.Teardown(context.Background(), env))

	res3, err := provider.Provision(context.Background(), env)
	require.NoError(t, err)
	assert.NotEqual(t, res1.DSN, res3.DSN, "after Teardown a fresh password should be generated")
}

func TestSchemaDatabaseProvider_Security_SQLInjectionInEnvironmentNames(t *testing.T) {
	provider := NewSchemaDatabaseProvider("postgres://admin@localhost/db")
	provider.Executor = &recordingExecutor{}

	payloads := []string{
		"'; DROP SCHEMA public CASCADE; --",
		"env\" OR \"1\"=\"1",
		"-leading-dash",
		"_leading_underscore",
		"env$dollar",
		"env\x00null",
		"env space",
	}

	for _, payload := range payloads {
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: payload},
		}
		_, err := provider.Provision(context.Background(), env)
		assert.ErrorIs(t, err, ErrInvalidSchemaName, "payload %q should be rejected", payload)

		err = provider.Teardown(context.Background(), env)
		assert.ErrorIs(t, err, ErrInvalidSchemaName, "teardown payload %q should be rejected", payload)
	}
}

func TestSchemaDatabaseProvider_Security_DifferentEnvsHaveDistinctCredentials(t *testing.T) {
	provider := NewSchemaDatabaseProvider("postgres://admin@localhost/db")
	provider.Executor = &recordingExecutor{}

	envA := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "env-a"}}
	envB := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "env-b"}}

	resA, err := provider.Provision(context.Background(), envA)
	require.NoError(t, err)
	resB, err := provider.Provision(context.Background(), envB)
	require.NoError(t, err)

	assert.NotEqual(t, resA.DSN, resB.DSN)
	assert.True(t, strings.Contains(resA.DSN, "diverge_preview_preview_env_a"))
	assert.True(t, strings.Contains(resB.DSN, "diverge_preview_preview_env_b"))
}
