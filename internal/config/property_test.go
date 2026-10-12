package config

import (
	"fmt"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/require"
	"github.com/xeipuuv/gojsonschema"
	"gopkg.in/yaml.v3"
	"hegel.dev/go/hegel"
	k8syaml "sigs.k8s.io/yaml"
)

func TestConfigResolve_PrecedenceAndIsolation_PBT(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		defaultDeployMode := hegel.Draw(ht, hegel.SampledFrom([]string{"delta", "full"}))
		envDeployMode := hegel.Draw(ht, hegel.SampledFrom([]string{"delta", "full"}))
		labelDeployMode := hegel.Draw(ht, hegel.SampledFrom([]string{"delta", "full"}))

		defaultTTL := hegel.Draw(ht, hegel.SampledFrom([]string{"1h", "2h", "4h"}))
		envTTL := hegel.Draw(ht, hegel.SampledFrom([]string{"8h", "12h", "24h"}))

		defaultDBMode := hegel.Draw(ht, hegel.SampledFrom([]string{"shared", "schema"}))
		envDBMode := hegel.Draw(ht, hegel.SampledFrom([]string{"schema", "snapshot"}))
		labelDBMode := hegel.Draw(ht, hegel.SampledFrom([]string{"snapshot", "fresh"}))

		flagValDefault := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(20))
		flagValEnv := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(20))
		flagValLabel := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(20))

		cfg := &Config{
			Version: "1",
			Defaults: EnvironmentSettings{
				Deploy: DeploySettings{
					Mode:      defaultDeployMode,
					Namespace: "same",
				},
				Routing: RoutingSettings{
					Mode:      "header",
					HeaderKey: "x-diverge-env",
					Banner: &BannerSettings{
						Text:     "Default Banner",
						Position: "top",
					},
				},
				Database: DatabaseSettings{
					Mode: defaultDBMode,
					Atlas: &AtlasSettings{
						Mode:      "versioned",
						ExtraArgs: []string{"--default-arg"},
					},
				},
				Lifecycle: LifecycleSettings{
					TTL: defaultTTL,
				},
				Features: &FeatureSettings{
					Provider: "configmap",
					Overrides: map[string]string{
						"flag_a": flagValDefault,
						"flag_b": "default-b",
					},
				},
			},
			Environments: map[string]EnvironmentType{
				"preview": {
					EnvironmentSettings: EnvironmentSettings{
						Deploy: DeploySettings{
							Mode: envDeployMode,
						},
						Database: DatabaseSettings{
							Mode: envDBMode,
							Atlas: &AtlasSettings{
								ExtraArgs: []string{"--env-arg"},
							},
						},
						Lifecycle: LifecycleSettings{
							TTL: envTTL,
						},
						Features: &FeatureSettings{
							Overrides: map[string]string{
								"flag_a": flagValEnv,
							},
						},
					},
				},
			},
			LabelOverrides: map[string]LabelOverride{
				"isolated-db": {
					EnvironmentSettings: EnvironmentSettings{
						Deploy: DeploySettings{
							Mode: labelDeployMode,
						},
						Routing: RoutingSettings{
							Banner: &BannerSettings{
								Text: "Label Banner",
							},
						},
						Database: DatabaseSettings{
							Mode: labelDBMode,
						},
						Features: &FeatureSettings{
							Overrides: map[string]string{
								"flag_a": flagValLabel,
							},
						},
					},
				},
			},
		}

		// 1. Defaults only (unmatched environment type and no labels)
		resDefaults := cfg.Resolve("unmatched", nil)
		require.Equal(ht, defaultDeployMode, resDefaults.Deploy.Mode)
		require.Equal(ht, defaultDBMode, resDefaults.Database.Mode)
		require.Equal(ht, defaultTTL, resDefaults.Lifecycle.TTL)
		require.Equal(ht, flagValDefault, resDefaults.Features.Overrides["flag_a"])

		// 2. EnvironmentType overrides Defaults while preserving unset Defaults
		resEnv := cfg.Resolve("preview", nil)
		require.Equal(ht, envDeployMode, resEnv.Deploy.Mode)
		require.Equal(ht, "same", resEnv.Deploy.Namespace)
		require.Equal(ht, envDBMode, resEnv.Database.Mode)
		require.Equal(ht, envTTL, resEnv.Lifecycle.TTL)
		require.Equal(ht, flagValEnv, resEnv.Features.Overrides["flag_a"])
		require.Equal(ht, "default-b", resEnv.Features.Overrides["flag_b"])

		// 3. LabelMatch overrides EnvironmentType which overrides Defaults
		resLabel := cfg.Resolve("preview", []string{"isolated-db"})
		require.Equal(ht, labelDeployMode, resLabel.Deploy.Mode)
		require.Equal(ht, labelDBMode, resLabel.Database.Mode)
		require.Equal(ht, envTTL, resLabel.Lifecycle.TTL)
		require.Equal(ht, "same", resLabel.Deploy.Namespace)
		require.Equal(ht, flagValLabel, resLabel.Features.Overrides["flag_a"])
		require.Equal(ht, "default-b", resLabel.Features.Overrides["flag_b"])
		require.Equal(ht, "Label Banner", resLabel.Routing.Banner.Text)
		require.Equal(ht, "top", resLabel.Routing.Banner.Position)

		// 4. Isolation: mutating returned settings must not mutate Config
		resLabel.Deploy.Mode = "mutated-mode"
		resLabel.Routing.Banner.Text = "mutated-banner"
		resLabel.Database.Atlas.ExtraArgs[0] = "--mutated"
		resLabel.Features.Overrides["flag_a"] = "mutated-flag"
		resLabel.Features.Overrides["flag_b"] = "mutated-b"

		require.Equal(ht, defaultDeployMode, cfg.Defaults.Deploy.Mode)
		require.Equal(ht, "Default Banner", cfg.Defaults.Routing.Banner.Text)
		require.Equal(ht, "--default-arg", cfg.Defaults.Database.Atlas.ExtraArgs[0])
		require.Equal(ht, "--env-arg", cfg.Environments["preview"].Database.Atlas.ExtraArgs[0])
		require.Equal(ht, flagValDefault, cfg.Defaults.Features.Overrides["flag_a"])
		require.Equal(ht, "default-b", cfg.Defaults.Features.Overrides["flag_b"])
		require.Equal(ht, flagValEnv, cfg.Environments["preview"].Features.Overrides["flag_a"])
		require.Equal(ht, flagValLabel, cfg.LabelOverrides["isolated-db"].Features.Overrides["flag_a"])
	})
}

func TestConfigSchema_ValidConfigsPassBothSchemaAndParse_PBT(t *testing.T) {
	schemaBytes, err := GenerateJSONSchema()
	require.NoError(t, err)
	schemaLoader := gojsonschema.NewBytesLoader(schemaBytes)

	hegel.Test(t, func(ht *hegel.T) {
		svcName := hegel.Draw(ht, hegel.FromRegex(`^[a-z][a-z0-9-]{1,12}$`, true))
		depName := hegel.Draw(ht, hegel.FromRegex(`^[a-z][a-z0-9-]{1,12}$`, true))
		deployMode := hegel.Draw(ht, hegel.SampledFrom([]string{"delta", "full"}))
		deployNS := hegel.Draw(ht, hegel.SampledFrom([]string{"same", "create"}))
		routingMode := hegel.Draw(ht, hegel.SampledFrom([]string{"header", "namespace", "subdomain"}))
		bannerPos := hegel.Draw(ht, hegel.SampledFrom([]string{"top", "bottom"}))
		bannerColor := hegel.Draw(ht, hegel.FromRegex(`^#[0-9a-fA-F]{6}$`, true))
		dbMode := hegel.Draw(ht, hegel.SampledFrom([]string{"shared", "schema", "snapshot", "fresh"}))
		atlasMode := hegel.Draw(ht, hegel.SampledFrom([]string{"versioned", "declarative"}))
		atlasEngine := hegel.Draw(ht, hegel.SampledFrom([]string{"operator", "job"}))
		atlasDestructive := hegel.Draw(ht, hegel.SampledFrom([]string{"error", "warn", "allow"}))
		featureProvider := hegel.Draw(ht, hegel.SampledFrom([]string{"configmap", "flipt", "flagsmith", "unleash", "noop", "none"}))
		devConflict := hegel.Draw(ht, hegel.SampledFrom([]string{"warn", "block", "allow"}))
		notifProvider := hegel.Draw(ht, hegel.SampledFrom([]string{"github", "gitlab"}))
		asyncProtocol := hegel.Draw(ht, hegel.SampledFrom([]string{"temporal", "kafka"}))
		includePrometheus := hegel.Draw(ht, hegel.Booleans())

		enabled := true
		cfg := Config{
			Version: "1",
			Services: map[string]ServiceConfig{
				svcName: {
					Paths: []string{fmt.Sprintf("services/%s/**", svcName)},
					Image: ImageConfig{
						Repository:  fmt.Sprintf("ghcr.io/example/%s", svcName),
						TagTemplate: "mr-{{.MR}}",
					},
					DependsOn:  []string{depName},
					Entrypoint: true,
					AsyncRoutes: []v1alpha1.AsyncRouteSpec{
						{
							Protocol: asyncProtocol,
							Target:   "events-queue",
							EnvVarMapping: map[string]string{
								"events-queue": "QUEUE_NAME",
							},
						},
					},
				},
			},
			Defaults: EnvironmentSettings{
				Deploy: DeploySettings{
					Mode:      deployMode,
					Namespace: deployNS,
				},
				Routing: RoutingSettings{
					Mode:              routingMode,
					BaselineNamespace: "default",
					HeaderKey:         "x-diverge-env",
					Domain:            "preview.example.com",
					Banner: &BannerSettings{
						Enabled:  &enabled,
						Text:     "Preview",
						Position: bannerPos,
						Color:    bannerColor,
					},
				},
				Database: DatabaseSettings{
					Mode:          dbMode,
					ConnectionRef: "db-secret",
					Atlas: &AtlasSettings{
						Mode:   atlasMode,
						Engine: atlasEngine,
						Policy: &AtlasPolicySettings{
							Destructive: atlasDestructive,
						},
					},
				},
				Lifecycle: LifecycleSettings{
					TTL:            "24h",
					CleanupOnMerge: &enabled,
				},
				Features: &FeatureSettings{
					Provider: featureProvider,
					Overrides: map[string]string{
						"new_ui": "true",
					},
				},
				Dev: &DevSettings{
					OnConflict: devConflict,
				},
			},
			Notifications: NotificationConfig{
				Provider:        notifProvider,
				CommentOnCreate: true,
				CommentOnReady:  true,
			},
		}
		if includePrometheus {
			cfg.Topology = TopologyConfig{
				Prometheus: &PrometheusConfig{
					Address:        "http://prometheus:9090",
					TokenEnv:       "PROM_TOKEN",
					PollInterval:   "30s",
					CacheTTL:       "5m",
					MeshType:       "istio",
					LookbackWindow: "10m",
					Namespaces:     []string{"default"},
				},
			}
		}

		yamlBytes, err := yaml.Marshal(&cfg)
		require.NoError(ht, err)

		parsed, err := Parse(yamlBytes)
		require.NoError(ht, err)
		require.NoError(ht, parsed.Validate())

		jsonBytes, err := k8syaml.YAMLToJSON(yamlBytes)
		require.NoError(ht, err)

		res, err := gojsonschema.Validate(schemaLoader, gojsonschema.NewBytesLoader(jsonBytes))
		require.NoError(ht, err)
		require.True(ht, res.Valid(), "expected generated config to pass JSON Schema, errors: %v", res.Errors())
	})
}

func TestConfigSchema_UnknownFieldsAlwaysRejected_PBT(t *testing.T) {
	schemaBytes, err := GenerateJSONSchema()
	require.NoError(t, err)
	schemaLoader := gojsonschema.NewBytesLoader(schemaBytes)

	knownFields := map[string]bool{
		"version": true, "services": true, "defaults": true, "environments": true,
		"label_overrides": true, "notifications": true, "topology": true,
		"mode": true, "namespace": true, "paths": true, "image": true, "helm": true,
		"asyncRoutes": true, "dependsOn": true, "entrypoint": true,
	}

	hegel.Test(t, func(ht *hegel.T) {
		unknownKey := hegel.Draw(ht, hegel.FromRegex(`^[a-z][a-z0-9_]{3,16}$`, true))
		if knownFields[unknownKey] {
			return
		}
		targetSection := hegel.Draw(ht, hegel.SampledFrom([]string{"root", "deploy", "service"}))

		var rawYAML string
		switch targetSection {
		case "root":
			rawYAML = fmt.Sprintf("version: \"1\"\n%s: \"unexpected\"\n", unknownKey)
		case "deploy":
			rawYAML = fmt.Sprintf("version: \"1\"\ndefaults:\n  deploy:\n    mode: delta\n    %s: \"unexpected\"\n", unknownKey)
		case "service":
			rawYAML = fmt.Sprintf("version: \"1\"\nservices:\n  api:\n    paths: [\"src/**\"]\n    image:\n      repository: \"ghcr.io/example/api\"\n    %s: \"unexpected\"\n", unknownKey)
		}

		_, parseErr := Parse([]byte(rawYAML))
		require.Error(ht, parseErr, "expected config.Parse to reject unknown field %q in %s", unknownKey, targetSection)

		jsonBytes, err := k8syaml.YAMLToJSON([]byte(rawYAML))
		require.NoError(ht, err)

		res, err := gojsonschema.Validate(schemaLoader, gojsonschema.NewBytesLoader(jsonBytes))
		require.NoError(ht, err)
		require.False(ht, res.Valid(), "expected JSON Schema (additionalProperties: false) to reject unknown field %q in %s", unknownKey, targetSection)
	})
}
