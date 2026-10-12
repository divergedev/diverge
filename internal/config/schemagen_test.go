package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xeipuuv/gojsonschema"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8syaml "sigs.k8s.io/yaml"
)

func TestGenerateJSONSchema_MatchesCommittedSchemas(t *testing.T) {
	generated, err := GenerateJSONSchema()
	require.NoError(t, err)
	require.NotEmpty(t, generated)

	root := projectRoot()
	cliSchemaPath := filepath.Join(root, "internal", "cli", "schema", "diverge-config.schema.json")
	pubSchemaPath := filepath.Join(root, "schemas", "diverge.schema.json")

	if os.Getenv("UPDATE_SCHEMA") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(cliSchemaPath), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Dir(pubSchemaPath), 0o755))
		require.NoError(t, os.WriteFile(cliSchemaPath, generated, 0o644))
		require.NoError(t, os.WriteFile(pubSchemaPath, generated, 0o644))
	}

	cliSchemaBytes, err := os.ReadFile(cliSchemaPath)
	require.NoError(t, err, "failed to read %s", cliSchemaPath)
	assert.Equal(t, string(generated), string(cliSchemaBytes), "internal/cli/schema/diverge-config.schema.json is out of sync with GenerateJSONSchema()")

	pubSchemaBytes, err := os.ReadFile(pubSchemaPath)
	require.NoError(t, err, "failed to read %s", pubSchemaPath)
	assert.Equal(t, string(generated), string(pubSchemaBytes), "schemas/diverge.schema.json is out of sync with GenerateJSONSchema()")
}

func TestGenerateJSONSchema_ValidatesSampleDivergeYAML(t *testing.T) {
	schemaBytes, err := GenerateJSONSchema()
	require.NoError(t, err)

	samplePath := filepath.Join(projectRoot(), "config", "samples", "diverge.yaml")
	cfg, err := Load(samplePath)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	yamlBytes, err := os.ReadFile(samplePath)
	require.NoError(t, err)

	jsonBytes, err := k8syaml.YAMLToJSON(yamlBytes)
	require.NoError(t, err)

	res, err := gojsonschema.Validate(
		gojsonschema.NewBytesLoader(schemaBytes),
		gojsonschema.NewBytesLoader(jsonBytes),
	)
	require.NoError(t, err)
	assert.True(t, res.Valid(), "expected config/samples/diverge.yaml to pass JSON Schema validation, errors: %v", res.Errors())
}

func TestGenerateJSONSchema_ValidatesFullFeatureConfig(t *testing.T) {
	schemaBytes, err := GenerateJSONSchema()
	require.NoError(t, err)

	fullYAML := strings.Join([]string{
		`version: "1"`,
		`services:`,
		`  gateway:`,
		`    paths: ["services/gateway/**"]`,
		`    image:`,
		`      repository: "registry.example.com/gateway"`,
		`      tag_template: "mr-{{.MR}}"`,
		`    entrypoint: true`,
		`    dependsOn: ["payments-api"]`,
		`  payments-api:`,
		`    paths: ["services/payments/**"]`,
		`    image:`,
		`      repository: "registry.example.com/payments-api"`,
		`    helm:`,
		`      path: "charts/payments-api"`,
		`      values_file: "values-preview.yaml"`,
		`    asyncRoutes:`,
		`      - protocol: temporal`,
		`        target: payments-queue`,
		`        envVarMapping:`,
		`          payments-queue: TEMPORAL_TASK_QUEUE`,
		`defaults:`,
		`  deploy:`,
		`    mode: delta`,
		`    namespace: same`,
		`  routing:`,
		`    mode: header`,
		`    baseline_namespace: staging`,
		`    header_key: x-diverge-env`,
		`    domain: preview.example.com`,
		`    banner:`,
		`      enabled: true`,
		`      text: "Preview Environment"`,
		`      position: top`,
		`      color: "#FF6B00"`,
		`  database:`,
		`    mode: schema`,
		`    connection_ref: db-secret`,
		`    atlas:`,
		`      mode: declarative`,
		`      engine: operator`,
		`      schema_config_map: payments-schema`,
		`      blocking: true`,
		`      policy:`,
		`        destructive: error`,
		`  lifecycle:`,
		`    ttl: 72h`,
		`    cleanup_on_merge: true`,
		`  features:`,
		`    provider: flipt`,
		`    connection_ref: flipt-secret`,
		`    overrides:`,
		`      new_checkout: "true"`,
		`  dev:`,
		`    on_conflict: warn`,
		`topology:`,
		`  prometheus:`,
		`    address: "http://prometheus.monitoring.svc:9090"`,
		`    tokenEnv: "PROM_TOKEN"`,
		`    pollInterval: "30s"`,
		`    cacheTTL: "5m"`,
		`    meshType: "istio"`,
		`    lookbackWindow: "10m"`,
		`    namespaces: ["default", "payments"]`,
	}, "\n")

	cfg, err := Parse([]byte(fullYAML))
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	jsonBytes, err := k8syaml.YAMLToJSON([]byte(fullYAML))
	require.NoError(t, err)

	res, err := gojsonschema.Validate(
		gojsonschema.NewBytesLoader(schemaBytes),
		gojsonschema.NewBytesLoader(jsonBytes),
	)
	require.NoError(t, err)
	assert.True(t, res.Valid(), "expected full config to pass JSON Schema validation, errors: %v", res.Errors())
}

func TestConfigSamples_UnmarshalStrict(t *testing.T) {
	samplesDir := filepath.Join(projectRoot(), "config", "samples")
	entries, err := os.ReadDir(samplesDir)
	require.NoError(t, err)

	var testedCount int
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		if entry.Name() == "diverge.yaml" {
			continue
		}

		samplePath := filepath.Join(samplesDir, entry.Name())
		t.Run(entry.Name(), func(t *testing.T) {
			data, err := os.ReadFile(samplePath)
			require.NoError(t, err)

			var meta metav1.TypeMeta
			require.NoError(t, k8syaml.Unmarshal(data, &meta))
			assert.Equal(t, "divergedev.com/v1alpha1", meta.APIVersion, "unexpected apiVersion in %s", entry.Name())

			switch meta.Kind {
			case "Environment":
				var obj v1alpha1.Environment
				err = k8syaml.UnmarshalStrict(data, &obj)
				require.NoError(t, err, "strict unmarshal failed for Environment sample %s", entry.Name())
				assert.NotEmpty(t, obj.Name)
			case "PreviewGroup":
				var obj v1alpha1.PreviewGroup
				err = k8syaml.UnmarshalStrict(data, &obj)
				require.NoError(t, err, "strict unmarshal failed for PreviewGroup sample %s", entry.Name())
				assert.NotEmpty(t, obj.Name)
				assert.NotEmpty(t, obj.Spec.Services)
			case "AgentTask":
				var obj v1alpha1.AgentTask
				err = k8syaml.UnmarshalStrict(data, &obj)
				require.NoError(t, err, "strict unmarshal failed for AgentTask sample %s", entry.Name())
				assert.NotEmpty(t, obj.Name)
				assert.NotEmpty(t, obj.Spec.Objective)
			default:
				t.Fatalf("unknown sample kind %q in %s", meta.Kind, entry.Name())
			}
		})
		testedCount++
	}

	assert.GreaterOrEqual(t, testedCount, 7, "expected at least 7 CR sample files in config/samples/")
}
