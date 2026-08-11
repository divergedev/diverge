package deployer

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/database"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testEnvWithDB(name string, dbMode string, connectionRef string, dbEnvKey string) *v1alpha1.Environment {
	return &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Database: v1alpha1.EnvironmentDatabase{
				Mode:          dbMode,
				ConnectionRef: connectionRef,
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName:    "payments-api",
				Port:           8080,
				Image:          "payments:v1",
				DatabaseEnvKey: dbEnvKey,
			},
		},
	}
}

func TestResolveDBSecret_ConnectionRef(t *testing.T) {
	env := testEnvWithDB("preview-42", "schema", "my-postgres-creds", "")
	secret := resolveDBSecret(env)
	assert.Equal(t, "my-postgres-creds", secret, "connectionRef should take priority")
}

func TestResolveDBSecret_SchemaMode(t *testing.T) {
	env := testEnvWithDB("preview-42", "schema", "", "")
	secret := resolveDBSecret(env)
	schemaName, err := database.SchemaName(env)
	require.NoError(t, err)
	expected := fmt.Sprintf("diverge-db-%s", schemaName)
	assert.Equal(t, expected, secret)
}

func TestResolveDBSecret_NoDatabase(t *testing.T) {
	env := testEnvWithDB("preview-42", "", "", "")
	secret := resolveDBSecret(env)
	assert.Empty(t, secret)
}

func TestResolveDBSecret_SharedMode(t *testing.T) {
	env := testEnvWithDB("preview-42", "shared", "", "")
	secret := resolveDBSecret(env)
	assert.Empty(t, secret, "shared mode should not inject DB secret")
}

func TestResolveDBSecret_ConnectionRefOverridesSchemaMode(t *testing.T) {
	env := testEnvWithDB("preview-42", "schema", "custom-secret", "")
	secret := resolveDBSecret(env)
	assert.Equal(t, "custom-secret", secret, "connectionRef should override schema auto-provision")
}

func TestServiceConfigFetcher_WithSchemaDB_InjectsEnvFrom(t *testing.T) {
	env := testEnvWithDB("preview-42", "schema", "", "")
	fetcher := &ServiceConfigFetcher{}
	objects, err := fetcher.Fetch(context.Background(), env)
	require.NoError(t, err)
	require.Len(t, objects, 2) // Deployment + Service

	deploy := objects[0]
	containers, found, _ := unstructuredNestedSlice(deploy.Object, "spec", "template", "spec", "containers")
	require.True(t, found)
	require.Len(t, containers, 1)

	container := containers[0].(map[string]interface{})
	envFrom, ok := container["envFrom"].([]interface{})
	require.True(t, ok, "envFrom should be present")
	require.Len(t, envFrom, 1)

	ref := envFrom[0].(map[string]interface{})
	secretRef := ref["secretRef"].(map[string]interface{})
	schemaName, err := database.SchemaName(env)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("diverge-db-%s", schemaName), secretRef["name"])
}

func TestServiceConfigFetcher_WithConnectionRef_InjectsEnvFrom(t *testing.T) {
	env := testEnvWithDB("preview-42", "schema", "my-db-secret", "")
	fetcher := &ServiceConfigFetcher{}
	objects, err := fetcher.Fetch(context.Background(), env)
	require.NoError(t, err)

	deploy := objects[0]
	containers, found, _ := unstructuredNestedSlice(deploy.Object, "spec", "template", "spec", "containers")
	require.True(t, found)
	container := containers[0].(map[string]interface{})
	envFrom := container["envFrom"].([]interface{})
	ref := envFrom[0].(map[string]interface{})
	secretRef := ref["secretRef"].(map[string]interface{})
	assert.Equal(t, "my-db-secret", secretRef["name"])
}

func TestServiceConfigFetcher_NoDatabase_NoEnvFrom(t *testing.T) {
	env := testEnvWithDB("preview-42", "", "", "")
	fetcher := &ServiceConfigFetcher{}
	objects, err := fetcher.Fetch(context.Background(), env)
	require.NoError(t, err)

	deploy := objects[0]
	containers, found, _ := unstructuredNestedSlice(deploy.Object, "spec", "template", "spec", "containers")
	require.True(t, found)
	container := containers[0].(map[string]interface{})
	envFrom, ok := container["envFrom"].([]interface{})
	// envFrom should be nil/empty when no DB configured
	if ok {
		assert.Empty(t, envFrom)
	}
}

func TestServiceConfigFetcher_CustomDatabaseEnvKey(t *testing.T) {
	env := testEnvWithDB("preview-42", "schema", "my-db-secret", "SPRING_DATASOURCE_URL")
	fetcher := &ServiceConfigFetcher{}
	objects, err := fetcher.Fetch(context.Background(), env)
	require.NoError(t, err)

	deploy := objects[0]
	containers, found, _ := unstructuredNestedSlice(deploy.Object, "spec", "template", "spec", "containers")
	require.True(t, found)
	container := containers[0].(map[string]interface{})

	// Should have the custom env key mapped from the secret
	envVars := container["env"].([]interface{})
	var foundCustom bool
	for _, ev := range envVars {
		e := ev.(map[string]interface{})
		if e["name"] == "SPRING_DATASOURCE_URL" {
			foundCustom = true
			valueFrom := e["valueFrom"].(map[string]interface{})
			secretKeyRef := valueFrom["secretKeyRef"].(map[string]interface{})
			assert.Equal(t, "my-db-secret", secretKeyRef["name"])
			assert.Equal(t, "DATABASE_URL", secretKeyRef["key"])
		}
	}
	assert.True(t, foundCustom, "SPRING_DATASOURCE_URL env var should exist")
}

// Helper to access nested slices in unstructured objects
func unstructuredNestedSlice(obj map[string]interface{}, fields ...string) ([]interface{}, bool, error) {
	current := obj
	for i, field := range fields {
		if i == len(fields)-1 {
			val, ok := current[field].([]interface{})
			return val, ok, nil
		}
		next, ok := current[field].(map[string]interface{})
		if !ok {
			return nil, false, nil
		}
		current = next
	}
	return nil, false, nil
}
