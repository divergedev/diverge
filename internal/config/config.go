package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/divergedev/diverge/api/v1alpha1"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Version        string                     `yaml:"version"`
	Services       map[string]ServiceConfig   `yaml:"services"`
	Defaults       EnvironmentSettings        `yaml:"defaults"`
	Environments   map[string]EnvironmentType `yaml:"environments"`
	LabelOverrides map[string]LabelOverride   `yaml:"label_overrides"`
	Notifications  NotificationConfig         `yaml:"notifications"`
}

var ErrConfigNotFound = errors.New("config not found")

type ServiceConfig struct {
	Paths       []string                  `yaml:"paths"`
	Image       ImageConfig               `yaml:"image"`
	Helm        *HelmConfig               `yaml:"helm,omitempty"`
	AsyncRoutes []v1alpha1.AsyncRouteSpec `yaml:"asyncRoutes,omitempty" json:"asyncRoutes,omitempty"`

	// DependsOn lists services that this service calls at runtime.
	// Used for topology graph resolution in composable environments.
	// Standard semantics: "A dependsOn B" means A calls B.
	DependsOn []string `yaml:"dependsOn,omitempty"`

	// Entrypoint marks this service as an ingress gateway.
	// Entrypoints are the starting points for ingress path resolution.
	Entrypoint bool `yaml:"entrypoint,omitempty"`
}

type ImageConfig struct {
	Repository  string `yaml:"repository"`
	TagTemplate string `yaml:"tag_template"`
}

type HelmConfig struct {
	Path       string `yaml:"path"`
	ValuesFile string `yaml:"values_file"`
}

type EnvironmentSettings struct {
	Deploy    DeploySettings    `yaml:"deploy"`
	Routing   RoutingSettings   `yaml:"routing"`
	Database  DatabaseSettings  `yaml:"database"`
	Lifecycle LifecycleSettings `yaml:"lifecycle"`
}

type DeploySettings struct {
	Mode      string `yaml:"mode"`      // delta | full
	Namespace string `yaml:"namespace"` // same | create
}

type RoutingSettings struct {
	Mode              string `yaml:"mode"` // header | namespace | subdomain
	BaselineNamespace string `yaml:"baseline_namespace"`
	HeaderKey         string `yaml:"header_key"`
	Domain            string `yaml:"domain"`
}

type DatabaseSettings struct {
	Mode             string `yaml:"mode"` // shared | schema | snapshot | fresh
	ConnectionRef    string `yaml:"connection_ref"`
	SeedSource       string `yaml:"seed_source"`
	MigrationCommand string `yaml:"migration_command"`
}

type LifecycleSettings struct {
	TTL            string `yaml:"ttl"`
	CleanupOnMerge *bool  `yaml:"cleanup_on_merge,omitempty"`
}

type EnvironmentType struct {
	EnvironmentSettings `yaml:",inline"`
	Trigger             string `yaml:"trigger"` // label | auto | manual | branch | schedule
	Label               string `yaml:"label"`
	Branch              string `yaml:"branch"`
}

type LabelOverride struct {
	EnvironmentSettings `yaml:",inline"`
	Skip                bool `yaml:"skip"`
}

type NotificationConfig struct {
	Provider         string `yaml:"provider"` // gitlab | github
	CommentOnCreate  bool   `yaml:"comment_on_create"`
	CommentOnReady   bool   `yaml:"comment_on_ready"`
	CommentOnDestroy bool   `yaml:"comment_on_destroy"`
}

// ResolvedSettings represents a fully resolved environment configuration
type ResolvedSettings struct {
	EnvironmentSettings
}

func Parse(data []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if c.Version != "1" {
		return nil, fmt.Errorf("unsupported version %q, expected \"1\"", c.Version)
	}

	return &c, nil
}

// Load reads the YAML file from the given path, unmarshals it, and validates the version.
func Load(path string) (*Config, error) {
	info, err := os.Stat(path)
	if err == nil && info.Size() > 1<<20 { // 1MB
		return nil, fmt.Errorf("config file too large: %d bytes (max 1MB)", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	return Parse(data)
}

// Resolve merges defaults with the named environment type, then applies label overrides in order.
func (c *Config) Resolve(envType string, labels []string) *ResolvedSettings {
	res := &ResolvedSettings{
		EnvironmentSettings: c.Defaults,
	}

	if env, ok := c.Environments[envType]; ok {
		mergeSettings(&res.EnvironmentSettings, &env.EnvironmentSettings)
	}

	for _, label := range labels {
		if override, ok := c.LabelOverrides[label]; ok {
			mergeSettings(&res.EnvironmentSettings, &override.EnvironmentSettings)
		}
	}

	return res
}

func mergeSettings(dst, src *EnvironmentSettings) {
	// Deploy
	if src.Deploy.Mode != "" {
		dst.Deploy.Mode = src.Deploy.Mode
	}
	if src.Deploy.Namespace != "" {
		dst.Deploy.Namespace = src.Deploy.Namespace
	}

	// Routing
	if src.Routing.Mode != "" {
		dst.Routing.Mode = src.Routing.Mode
	}
	if src.Routing.BaselineNamespace != "" {
		dst.Routing.BaselineNamespace = src.Routing.BaselineNamespace
	}
	if src.Routing.HeaderKey != "" {
		dst.Routing.HeaderKey = src.Routing.HeaderKey
	}
	if src.Routing.Domain != "" {
		dst.Routing.Domain = src.Routing.Domain
	}

	// Database
	if src.Database.Mode != "" {
		dst.Database.Mode = src.Database.Mode
	}
	if src.Database.ConnectionRef != "" {
		dst.Database.ConnectionRef = src.Database.ConnectionRef
	}
	if src.Database.SeedSource != "" {
		dst.Database.SeedSource = src.Database.SeedSource
	}
	if src.Database.MigrationCommand != "" {
		dst.Database.MigrationCommand = src.Database.MigrationCommand
	}

	// Lifecycle
	if src.Lifecycle.TTL != "" {
		dst.Lifecycle.TTL = src.Lifecycle.TTL
	}
	if src.Lifecycle.CleanupOnMerge != nil {
		dst.Lifecycle.CleanupOnMerge = src.Lifecycle.CleanupOnMerge
	}
}
