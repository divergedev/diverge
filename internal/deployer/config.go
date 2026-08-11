package deployer

import (
	"fmt"

	"github.com/divergedev/diverge/api/v1alpha1"
	"gopkg.in/yaml.v3"
)

// DotDivergeConfig represents the .diverge.yaml file found in service repositories.
type DotDivergeConfig struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec DotDivergeSpec `yaml:"spec"`
}

// DotDivergeSpec is the spec section of .diverge.yaml.
type DotDivergeSpec struct {
	Namespace   string              `yaml:"namespace"`
	ServiceName string              `yaml:"serviceName"`
	Port        int32               `yaml:"port"`
	Routing     DotDivergeRouting   `yaml:"routing"`
	Container   DotDivergeContainer `yaml:"container"`
}

// DotDivergeRouting configures preview routing.
type DotDivergeRouting struct {
	ParentRef string `yaml:"parentRef"`
	HeaderKey string `yaml:"headerKey"`
}

// DotDivergeContainer configures the preview container.
type DotDivergeContainer struct {
	Env []struct {
		Name  string `yaml:"name"`
		Value string `yaml:"value"`
	} `yaml:"env"`
}

// ParseDotDivergeConfig parses the raw bytes of a .diverge.yaml file.
func ParseDotDivergeConfig(data []byte) (*DotDivergeConfig, error) {
	var cfg DotDivergeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse .diverge.yaml: %w", err)
	}
	if cfg.Spec.ServiceName == "" {
		return nil, fmt.Errorf(".diverge.yaml: spec.serviceName is required")
	}
	if cfg.Spec.Port == 0 {
		cfg.Spec.Port = 8080 // default
	}
	if cfg.Spec.Port < 1 || cfg.Spec.Port > 65535 {
		return nil, fmt.Errorf("port %d out of valid range 1-65535", cfg.Spec.Port)
	}
	return &cfg, nil
}

// ToServicePreviewConfig converts a parsed .diverge.yaml into the CRD type.
func (c *DotDivergeConfig) ToServicePreviewConfig(image string) *v1alpha1.ServicePreviewConfig {
	cfg := &v1alpha1.ServicePreviewConfig{
		ServiceName: c.Spec.ServiceName,
		Namespace:   c.Spec.Namespace,
		Port:        c.Spec.Port,
		Image:       image,
		ParentRef:   c.Spec.Routing.ParentRef,
		HeaderKey:   c.Spec.Routing.HeaderKey,
	}
	for _, env := range c.Spec.Container.Env {
		cfg.Env = append(cfg.Env, v1alpha1.EnvVar{Name: env.Name, Value: env.Value})
	}
	return cfg
}
