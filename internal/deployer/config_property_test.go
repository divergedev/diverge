package deployer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"hegel.dev/go/hegel"
)

func genString(ht *hegel.T, chars []string, min, max int) string {
	length := hegel.Draw(ht, hegel.Integers(min, max))
	if length == 0 {
		return ""
	}
	res := ""
	for i := 0; i < length; i++ {
		res += hegel.Draw(ht, hegel.SampledFrom(chars))
	}
	return res
}

func genDNS1123(ht *hegel.T) string {
	chars := []string{"a", "b", "0", "1", "-"}
	first := hegel.Draw(ht, hegel.SampledFrom([]string{"a", "b", "0", "1"}))
	length := hegel.Draw(ht, hegel.Integers(0, 8))
	if length == 0 {
		return first
	}
	res := first
	for i := 0; i < length-1; i++ {
		res += hegel.Draw(ht, hegel.SampledFrom(chars))
	}
	res += hegel.Draw(ht, hegel.SampledFrom([]string{"a", "b", "0", "1"}))
	return res
}

func TestParseDotDivergeConfig_NoPanic(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		data := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(100))
		_, _ = ParseDotDivergeConfig([]byte(data))
	})
}

func TestParseDotDivergeConfig_Roundtrip(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		cfg := DotDivergeConfig{
			APIVersion: genString(ht, []string{"a", "A", "0", ".", "/"}, 0, 10),
			Kind:       genString(ht, []string{"a", "A", "0"}, 0, 10),
		}
		cfg.Metadata.Name = genString(ht, []string{"a", "A", "0", "-"}, 0, 10)
		cfg.Spec.Namespace = genString(ht, []string{"a", "A", "0", "-"}, 0, 10)
		cfg.Spec.ServiceName = genDNS1123(ht)
		cfg.Spec.Port = int32(hegel.Draw(ht, hegel.Integers(1, 65535)))
		cfg.Spec.Routing.ParentRef = genString(ht, []string{"a", "A", "0", "-"}, 0, 10)
		cfg.Spec.Routing.HeaderKey = genString(ht, []string{"a", "A", "0", "-"}, 0, 10)

		numEnvs := hegel.Draw(ht, hegel.Integers(0, 5))
		for i := 0; i < numEnvs; i++ {
			envNameFirst := hegel.Draw(ht, hegel.SampledFrom([]string{"A", "B", "_"}))
			envNameRest := genString(ht, []string{"A", "B", "0", "_"}, 0, 10)
			cfg.Spec.Container.Env = append(cfg.Spec.Container.Env, struct {
				Name  string `yaml:"name"`
				Value string `yaml:"value"`
			}{
				Name:  envNameFirst + envNameRest,
				Value: genString(ht, []string{"a", "A", "0", ".", "_", "/", "-"}, 0, 10),
			})
		}

		b, err := yaml.Marshal(&cfg)
		require.NoError(ht, err, "failed to marshal")

		parsed, err := ParseDotDivergeConfig(b)
		require.NoError(ht, err, "failed to parse marshaled config")

		assert.Equal(ht, cfg.APIVersion, parsed.APIVersion)
		assert.Equal(ht, cfg.Kind, parsed.Kind)
		assert.Equal(ht, cfg.Metadata.Name, parsed.Metadata.Name)
		assert.Equal(ht, cfg.Spec.ServiceName, parsed.Spec.ServiceName)
		assert.Equal(ht, cfg.Spec.Port, parsed.Spec.Port)
		assert.Equal(ht, cfg.Spec.Namespace, parsed.Spec.Namespace)
		assert.Equal(ht, cfg.Spec.Routing.ParentRef, parsed.Spec.Routing.ParentRef)
		assert.Equal(ht, cfg.Spec.Routing.HeaderKey, parsed.Spec.Routing.HeaderKey)

		require.Len(ht, parsed.Spec.Container.Env, len(cfg.Spec.Container.Env))
		for i, env := range cfg.Spec.Container.Env {
			assert.Equal(ht, env.Name, parsed.Spec.Container.Env[i].Name)
			assert.Equal(ht, env.Value, parsed.Spec.Container.Env[i].Value)
		}
	})
}

func TestToServicePreviewConfig_PreservesFields(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		svcName := genDNS1123(ht)
		port := int32(hegel.Draw(ht, hegel.Integers(1, 65535)))
		parentRef := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(20))
		headerKey := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(20))
		image := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(20))

		cfg := &DotDivergeConfig{}
		cfg.Spec.ServiceName = svcName
		cfg.Spec.Port = port
		cfg.Spec.Routing.ParentRef = parentRef
		cfg.Spec.Routing.HeaderKey = headerKey

		spc := cfg.ToServicePreviewConfig(image)

		require.Equalf(ht, svcName, spc.ServiceName, "ServiceName mismatch: got %q, want %q", spc.ServiceName, svcName)
		require.Equalf(ht, port, spc.Port, "Port mismatch: got %d, want %d", spc.Port, port)
		require.Equalf(ht, parentRef, spc.ParentRef, "ParentRef mismatch: got %q, want %q", spc.ParentRef, parentRef)
		require.Equalf(ht, headerKey, spc.HeaderKey, "HeaderKey mismatch: got %q, want %q", spc.HeaderKey, headerKey)
		require.Equalf(ht, image, spc.Image, "Image mismatch: got %q, want %q", spc.Image, image)
	})
}

func TestParseDotDivergeConfig_PortDefault(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		svcName := genDNS1123(ht)
		cfg := DotDivergeConfig{}
		cfg.Spec.ServiceName = svcName
		cfg.Spec.Port = 0

		b, err := yaml.Marshal(&cfg)
		require.NoError(ht, err, "failed to marshal")

		parsed, err := ParseDotDivergeConfig(b)
		require.NoError(ht, err, "failed to parse")

		require.Equalf(ht, int32(8080), parsed.Spec.Port, "expected port 8080, got %d", parsed.Spec.Port)
	})
}
