package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"

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
	Topology       TopologyConfig             `yaml:"topology,omitempty"`
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
	Features  *FeatureSettings  `yaml:"features,omitempty"`
	Dev       *DevSettings      `yaml:"dev,omitempty"`
}

type DeploySettings struct {
	Mode      string `yaml:"mode"`      // delta | full
	Namespace string `yaml:"namespace"` // same | create
}

type RoutingSettings struct {
	Mode              string          `yaml:"mode"` // header | namespace | subdomain
	BaselineNamespace string          `yaml:"baseline_namespace"`
	HeaderKey         string          `yaml:"header_key"`
	Domain            string          `yaml:"domain"`
	Banner            *BannerSettings `yaml:"banner,omitempty"`
}

// BannerSettings configures the visual preview environment indicator.
type BannerSettings struct {
	// Enabled controls whether the preview banner is injected. Defaults to true when banner is specified.
	Enabled  *bool  `yaml:"enabled,omitempty"`
	Text     string `yaml:"text,omitempty"`
	Position string `yaml:"position,omitempty"` // top | bottom
	Color    string `yaml:"color,omitempty"`    // hex color code
}

var hexColorRegex = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// Validate validates the BannerSettings.
// Position, if specified, must be "top" or "bottom".
// Color, if specified, must be a valid hex code (e.g. #RGB or #RRGGBB).
func (b *BannerSettings) Validate() error {
	if b == nil {
		return nil
	}
	if b.Position != "" && b.Position != "top" && b.Position != "bottom" {
		return fmt.Errorf("position must be \"top\" or \"bottom\", got %q", b.Position)
	}
	if b.Color != "" && !hexColorRegex.MatchString(b.Color) {
		return fmt.Errorf("color must be a valid hex code (e.g. #00FF00), got %q", b.Color)
	}
	return nil
}

type DatabaseSettings struct {
	Mode             string         `yaml:"mode"` // shared | schema | snapshot | fresh
	ConnectionRef    string         `yaml:"connection_ref"`
	SeedSource       string         `yaml:"seed_source"`
	MigrationCommand string         `yaml:"migration_command"`
	Atlas            *AtlasSettings `yaml:"atlas,omitempty"`
}

type AtlasSettings struct {
	Mode               string               `yaml:"mode"`             // versioned | declarative
	Engine             string               `yaml:"engine,omitempty"` // operator | job
	Image              string               `yaml:"image,omitempty"`
	MigrationConfigMap string               `yaml:"migration_config_map,omitempty"`
	SchemaConfigMap    string               `yaml:"schema_config_map,omitempty"`
	Blocking           *bool                `yaml:"blocking,omitempty"`
	Policy             *AtlasPolicySettings `yaml:"policy,omitempty"`
}

type AtlasPolicySettings struct {
	Destructive string `yaml:"destructive,omitempty"` // error | warn | allow
}

// Validate validates the AtlasSettings.
func (a *AtlasSettings) Validate() error {
	if a == nil {
		return nil
	}
	if a.Mode != "" && a.Mode != "versioned" && a.Mode != "declarative" {
		return fmt.Errorf("atlas mode must be \"versioned\" or \"declarative\", got %q", a.Mode)
	}
	if a.Engine != "" && a.Engine != "operator" && a.Engine != "job" {
		return fmt.Errorf("atlas engine must be \"operator\" or \"job\", got %q", a.Engine)
	}
	if a.Policy != nil && a.Policy.Destructive != "" {
		d := a.Policy.Destructive
		if d != "error" && d != "warn" && d != "allow" {
			return fmt.Errorf("atlas policy destructive must be \"error\", \"warn\", or \"allow\", got %q", d)
		}
	}
	return nil
}

type LifecycleSettings struct {
	TTL            string `yaml:"ttl"`
	CleanupOnMerge *bool  `yaml:"cleanup_on_merge,omitempty"`
}

type FeatureSettings struct {
	Provider      string            `yaml:"provider,omitempty"`
	Overrides     map[string]string `yaml:"overrides,omitempty"`
	ConnectionRef string            `yaml:"connection_ref,omitempty"`
}

func (f *FeatureSettings) Validate() error {
	if f == nil {
		return nil
	}
	if f.Provider != "" && f.Provider != "configmap" && f.Provider != "flipt" && f.Provider != "flagsmith" && f.Provider != "unleash" && f.Provider != "noop" && f.Provider != "none" {
		return fmt.Errorf("feature provider must be one of \"configmap\", \"flipt\", \"flagsmith\", \"unleash\", \"noop\", \"none\", got %q", f.Provider)
	}
	return nil
}

// DevSettings configures local development behavior (diverge dev).
type DevSettings struct {
	OnConflict string `yaml:"on_conflict,omitempty"` // warn | block | allow
}

func (d *DevSettings) Validate() error {
	if d == nil || d.OnConflict == "" {
		return nil
	}
	if d.OnConflict != "warn" && d.OnConflict != "block" && d.OnConflict != "allow" {
		return fmt.Errorf("dev on_conflict must be \"warn\", \"block\", or \"allow\", got %q", d.OnConflict)
	}
	return nil
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

type TopologyConfig struct {
	Prometheus *PrometheusConfig `yaml:"prometheus,omitempty"`
}

type PrometheusConfig struct {
	Address        string   `yaml:"address,omitempty"`
	TokenEnv       string   `yaml:"tokenEnv,omitempty"`
	TokenFile      string   `yaml:"tokenFile,omitempty"`
	CABundle       string   `yaml:"caBundle,omitempty"`
	InsecureTLS    bool     `yaml:"insecureTLS,omitempty"`
	PollInterval   string   `yaml:"pollInterval,omitempty"`
	CacheTTL       string   `yaml:"cacheTTL,omitempty"`
	MeshType       string   `yaml:"meshType,omitempty"`
	LookbackWindow string   `yaml:"lookbackWindow,omitempty"`
	Namespaces     []string `yaml:"namespaces,omitempty"`
}

// ResolvedSettings represents a fully resolved environment configuration
type ResolvedSettings struct {
	EnvironmentSettings
}

// Validate validates the configuration settings.
func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	if c.Defaults.Routing.Banner != nil {
		if err := c.Defaults.Routing.Banner.Validate(); err != nil {
			return fmt.Errorf("defaults.routing.banner: %w", err)
		}
	}
	if c.Defaults.Database.Atlas != nil {
		if err := c.Defaults.Database.Atlas.Validate(); err != nil {
			return fmt.Errorf("defaults.database.atlas: %w", err)
		}
	}
	if c.Defaults.Features != nil {
		if err := c.Defaults.Features.Validate(); err != nil {
			return fmt.Errorf("defaults.features: %w", err)
		}
	}
	if c.Defaults.Dev != nil {
		if err := c.Defaults.Dev.Validate(); err != nil {
			return fmt.Errorf("defaults.dev: %w", err)
		}
	}
	for name, env := range c.Environments {
		if env.Routing.Banner != nil {
			if err := env.Routing.Banner.Validate(); err != nil {
				return fmt.Errorf("environments[%s].routing.banner: %w", name, err)
			}
		}
		if env.Database.Atlas != nil {
			if err := env.Database.Atlas.Validate(); err != nil {
				return fmt.Errorf("environments[%s].database.atlas: %w", name, err)
			}
		}
		if env.Features != nil {
			if err := env.Features.Validate(); err != nil {
				return fmt.Errorf("environments[%s].features: %w", name, err)
			}
		}
		if env.Dev != nil {
			if err := env.Dev.Validate(); err != nil {
				return fmt.Errorf("environments[%s].dev: %w", name, err)
			}
		}
	}
	for label, override := range c.LabelOverrides {
		if override.Routing.Banner != nil {
			if err := override.Routing.Banner.Validate(); err != nil {
				return fmt.Errorf("label_overrides[%s].routing.banner: %w", label, err)
			}
		}
		if override.Database.Atlas != nil {
			if err := override.Database.Atlas.Validate(); err != nil {
				return fmt.Errorf("label_overrides[%s].database.atlas: %w", label, err)
			}
		}
		if override.Features != nil {
			if err := override.Features.Validate(); err != nil {
				return fmt.Errorf("label_overrides[%s].features: %w", label, err)
			}
		}
		if override.Dev != nil {
			if err := override.Dev.Validate(); err != nil {
				return fmt.Errorf("label_overrides[%s].dev: %w", label, err)
			}
		}
	}
	return nil
}

// Validate validates the resolved environment settings.
func (r *ResolvedSettings) Validate() error {
	if r == nil {
		return nil
	}
	if r.Routing.Banner != nil {
		if err := r.Routing.Banner.Validate(); err != nil {
			return err
		}
	}
	if r.Database.Atlas != nil {
		if err := r.Database.Atlas.Validate(); err != nil {
			return err
		}
	}
	if r.Features != nil {
		if err := r.Features.Validate(); err != nil {
			return err
		}
	}
	if r.Dev != nil {
		if err := r.Dev.Validate(); err != nil {
			return err
		}
	}
	return nil
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
	if src.Routing.Banner != nil {
		b := &BannerSettings{}
		if dst.Routing.Banner != nil {
			*b = *dst.Routing.Banner
		}
		if src.Routing.Banner.Enabled != nil {
			b.Enabled = src.Routing.Banner.Enabled
		}
		if src.Routing.Banner.Text != "" {
			b.Text = src.Routing.Banner.Text
		}
		if src.Routing.Banner.Position != "" {
			b.Position = src.Routing.Banner.Position
		}
		if src.Routing.Banner.Color != "" {
			b.Color = src.Routing.Banner.Color
		}
		dst.Routing.Banner = b
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
	if src.Database.Atlas != nil {
		a := &AtlasSettings{}
		if dst.Database.Atlas != nil {
			*a = *dst.Database.Atlas
		}
		if src.Database.Atlas.Mode != "" {
			a.Mode = src.Database.Atlas.Mode
		}
		if src.Database.Atlas.Engine != "" {
			a.Engine = src.Database.Atlas.Engine
		}
		if src.Database.Atlas.Image != "" {
			a.Image = src.Database.Atlas.Image
		}
		if src.Database.Atlas.MigrationConfigMap != "" {
			a.MigrationConfigMap = src.Database.Atlas.MigrationConfigMap
		}
		if src.Database.Atlas.SchemaConfigMap != "" {
			a.SchemaConfigMap = src.Database.Atlas.SchemaConfigMap
		}
		if src.Database.Atlas.Blocking != nil {
			a.Blocking = src.Database.Atlas.Blocking
		}
		if src.Database.Atlas.Policy != nil {
			p := &AtlasPolicySettings{}
			if a.Policy != nil {
				*p = *a.Policy
			}
			if src.Database.Atlas.Policy.Destructive != "" {
				p.Destructive = src.Database.Atlas.Policy.Destructive
			}
			a.Policy = p
		}
		dst.Database.Atlas = a
	}

	// Lifecycle
	if src.Lifecycle.TTL != "" {
		dst.Lifecycle.TTL = src.Lifecycle.TTL
	}
	if src.Lifecycle.CleanupOnMerge != nil {
		dst.Lifecycle.CleanupOnMerge = src.Lifecycle.CleanupOnMerge
	}

	// Features
	if src.Features != nil {
		f := &FeatureSettings{}
		if dst.Features != nil {
			*f = *dst.Features
			if dst.Features.Overrides != nil {
				f.Overrides = make(map[string]string, len(dst.Features.Overrides))
				for k, v := range dst.Features.Overrides {
					f.Overrides[k] = v
				}
			}
		}
		if src.Features.Provider != "" {
			f.Provider = src.Features.Provider
		}
		if src.Features.ConnectionRef != "" {
			f.ConnectionRef = src.Features.ConnectionRef
		}
		if len(src.Features.Overrides) > 0 {
			if f.Overrides == nil {
				f.Overrides = make(map[string]string, len(src.Features.Overrides))
			}
			for k, v := range src.Features.Overrides {
				f.Overrides[k] = v
			}
		}
		dst.Features = f
	}

	// Dev
	if src.Dev != nil {
		d := &DevSettings{}
		if dst.Dev != nil {
			*d = *dst.Dev
		}
		if src.Dev.OnConflict != "" {
			d.OnConflict = src.Dev.OnConflict
		}
		dst.Dev = d
	}
}
