//go:build !no_schema

package schemaprovider

import (
	"context"
	"strings"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
	assert.NotContains(t, res1.SetupSQL, "ALTER ROLE", "reconciliation must not rotate an already-issued role password")

	require.NoError(t, provider.Teardown(context.Background(), env))

	res3, err := provider.Provision(context.Background(), env)
	require.NoError(t, err)
	assert.NotEqual(t, res1.DSN, res3.DSN, "after Teardown a fresh password should be generated")
}

func TestSchemaDatabaseProvider_PasswordSurvivesProviderRestart(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	k8sClient := fake.NewClientBuilder().WithScheme(s).Build()

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "restart-env-1",
			Namespace: "default",
		},
	}

	// 1st provider instance provisions schema and persists role password in Kubernetes Secret
	p1 := NewSchemaDatabaseProvider("postgres://admin:supersecret@localhost:5432/appdb")
	p1.Executor = &recordingExecutor{}
	p1.Client = k8sClient

	res1, err := p1.Provision(ctx, env)
	require.NoError(t, err)

	// Simulate provider restart with fresh in-memory cache (and cleared package fallback to prove K8s Secret persistence)
	durablePasswords.Range(func(k, _ any) bool {
		durablePasswords.Delete(k)
		return true
	})

	p2 := NewSchemaDatabaseProvider("postgres://admin:supersecret@localhost:5432/appdb")
	p2.Executor = &recordingExecutor{}
	p2.Client = k8sClient

	res2, err := p2.Provision(ctx, env)
	require.NoError(t, err)
	assert.Equal(t, res1.DSN, res2.DSN, "restarted provider must load and reuse role password from durable Secret")

	// Teardown deletes the Secret so next Provision generates a fresh password
	require.NoError(t, p2.Teardown(ctx, env))

	var secList corev1.SecretList
	require.NoError(t, k8sClient.List(ctx, &secList))
	assert.Empty(t, secList.Items, "role password Secret should be deleted on Teardown")

	p3 := NewSchemaDatabaseProvider("postgres://admin:supersecret@localhost:5432/appdb")
	p3.Executor = &recordingExecutor{}
	p3.Client = k8sClient

	res3, err := p3.Provision(ctx, env)
	require.NoError(t, err)
	assert.NotEqual(t, res1.DSN, res3.DSN, "fresh password must be generated after Teardown")
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
