package deployer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"pgregory.net/rapid"
)

func TestParseDotDivergeConfig_NoPanic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		data := rapid.SliceOf(rapid.Byte()).Draw(t, "data")
		_, _ = ParseDotDivergeConfig(data)
	})
}

func TestParseDotDivergeConfig_Roundtrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cfg := DotDivergeConfig{
			APIVersion: rapid.StringMatching(`^[a-zA-Z0-9./]*$`).Draw(t, "APIVersion"),
			Kind:       rapid.StringMatching(`^[a-zA-Z0-9]*$`).Draw(t, "Kind"),
		}
		cfg.Metadata.Name = rapid.StringMatching(`^[a-zA-Z0-9-]*$`).Draw(t, "Name")
		cfg.Spec.Namespace = rapid.StringMatching(`^[a-zA-Z0-9-]*$`).Draw(t, "Namespace")
		// generate valid DNS-1123 name for serviceName
		cfg.Spec.ServiceName = rapid.StringMatching(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).Draw(t, "ServiceName")
		cfg.Spec.Port = rapid.Int32Range(1, 65535).Draw(t, "Port")
		cfg.Spec.Routing.ParentRef = rapid.StringMatching(`^[a-zA-Z0-9-]*$`).Draw(t, "ParentRef")
		cfg.Spec.Routing.HeaderKey = rapid.StringMatching(`^[a-zA-Z0-9-]*$`).Draw(t, "HeaderKey")

		numEnvs := rapid.IntRange(0, 5).Draw(t, "numEnvs")
		for i := 0; i < numEnvs; i++ {
			cfg.Spec.Container.Env = append(cfg.Spec.Container.Env, struct {
				Name  string `yaml:"name"`
				Value string `yaml:"value"`
			}{
				Name:  rapid.StringMatching(`^[A-Z_][A-Z0-9_]*$`).Draw(t, "EnvName"),
				Value: rapid.StringMatching(`^[a-zA-Z0-9._/-]*$`).Draw(t, "EnvValue"),
			})
		}

		b, err := yaml.Marshal(&cfg)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		parsed, err := ParseDotDivergeConfig(b)
		require.NoError(t, err, "failed to parse marshaled config")

		assert.Equal(t, cfg.APIVersion, parsed.APIVersion)
		assert.Equal(t, cfg.Kind, parsed.Kind)
		assert.Equal(t, cfg.Metadata.Name, parsed.Metadata.Name)
		assert.Equal(t, cfg.Spec.ServiceName, parsed.Spec.ServiceName)
		assert.Equal(t, cfg.Spec.Port, parsed.Spec.Port)
		assert.Equal(t, cfg.Spec.Namespace, parsed.Spec.Namespace)
		assert.Equal(t, cfg.Spec.Routing.ParentRef, parsed.Spec.Routing.ParentRef)
		assert.Equal(t, cfg.Spec.Routing.HeaderKey, parsed.Spec.Routing.HeaderKey)

		require.Len(t, parsed.Spec.Container.Env, len(cfg.Spec.Container.Env))
		for i, env := range cfg.Spec.Container.Env {
			assert.Equal(t, env.Name, parsed.Spec.Container.Env[i].Name)
			assert.Equal(t, env.Value, parsed.Spec.Container.Env[i].Value)
		}
	})
}

func TestToServicePreviewConfig_PreservesFields(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		svcName := rapid.StringMatching(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).Draw(t, "ServiceName")
		port := rapid.Int32Range(1, 65535).Draw(t, "Port")
		parentRef := rapid.String().Draw(t, "ParentRef")
		headerKey := rapid.String().Draw(t, "HeaderKey")
		image := rapid.String().Draw(t, "Image")

		cfg := &DotDivergeConfig{}
		cfg.Spec.ServiceName = svcName
		cfg.Spec.Port = port
		cfg.Spec.Routing.ParentRef = parentRef
		cfg.Spec.Routing.HeaderKey = headerKey

		spc := cfg.ToServicePreviewConfig(image)

		if spc.ServiceName != svcName {
			t.Fatalf("ServiceName mismatch: got %q, want %q", spc.ServiceName, svcName)
		}
		if spc.Port != port {
			t.Fatalf("Port mismatch: got %d, want %d", spc.Port, port)
		}
		if spc.ParentRef != parentRef {
			t.Fatalf("ParentRef mismatch: got %q, want %q", spc.ParentRef, parentRef)
		}
		if spc.HeaderKey != headerKey {
			t.Fatalf("HeaderKey mismatch: got %q, want %q", spc.HeaderKey, headerKey)
		}
		if spc.Image != image {
			t.Fatalf("Image mismatch: got %q, want %q", spc.Image, image)
		}
	})
}

func TestParseDotDivergeConfig_PortDefault(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		svcName := rapid.StringMatching(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).Draw(t, "ServiceName")
		cfg := DotDivergeConfig{}
		cfg.Spec.ServiceName = svcName
		cfg.Spec.Port = 0

		b, err := yaml.Marshal(&cfg)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		parsed, err := ParseDotDivergeConfig(b)
		if err != nil {
			t.Fatalf("failed to parse: %v", err)
		}

		if parsed.Spec.Port != 8080 {
			t.Fatalf("expected port 8080, got %d", parsed.Spec.Port)
		}
	})
}
