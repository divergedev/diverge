package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
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
