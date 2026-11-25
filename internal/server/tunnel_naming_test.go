package server

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
)

var validDNS1123Label = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]{0,61}[a-z0-9])?$`)

func sanitizeDNSLabelPart(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "a"
	}
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], "-")
		if out == "" {
			return "a"
		}
	}
	return out
}

func TestTunnelResourceName_PBT(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		prefix := hegel.Draw(ht, hegel.SampledFrom([]string{"diverge-tunnel", "diverge-tunnel-lease"}))
		pRaw := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(63))
		sRaw := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(63))

		previewID := sanitizeDNSLabelPart(pRaw)
		service := ""
		if sRaw != "" {
			service = sanitizeDNSLabelPart(sRaw)
		}

		got := tunnelResourceName(prefix, previewID, service)
		if len(got) > 63 {
			t.Fatalf("tunnelResourceName(%q, %q, %q) exceeded 63 chars: %d (%q)", prefix, previewID, service, len(got), got)
		}
		if !validDNS1123Label.MatchString(got) {
			t.Fatalf("tunnelResourceName(%q, %q, %q) = %q is not a valid DNS-1123 label", prefix, previewID, service, got)
		}

		for _, fn := range []struct {
			name string
			call func(string, string) string
		}{
			{name: "tunnelServiceName", call: tunnelServiceName},
			{name: "tunnelLeaseName", call: tunnelLeaseName},
		} {
			res := fn.call(previewID, service)
			if len(res) > 63 {
				t.Fatalf("%s(%q, %q) exceeded 63 chars: %d (%q)", fn.name, previewID, service, len(res), res)
			}
			if !validDNS1123Label.MatchString(res) {
				t.Fatalf("%s(%q, %q) = %q is not a valid DNS-1123 label", fn.name, previewID, service, res)
			}
			if service != "" {
				if unnamed := fn.call(previewID+"-"+service, ""); res == unnamed {
					t.Fatalf("%s named (%q, %q) collided with unnamed %q", fn.name, previewID, service, unnamed)
				}
			}
		}
	})
}

func TestTunnelResourceName_MaxLengthAndCollisionResistance(t *testing.T) {
	maxLabel := strings.Repeat("a", 63)
	for _, prefix := range []string{"diverge-tunnel", "diverge-tunnel-lease"} {
		res := tunnelResourceName(prefix, maxLabel, maxLabel)
		assert.LessOrEqual(t, len(res), 63)
		assert.True(t, validDNS1123Label.MatchString(res), "got %q", res)

		// Hyphenated split pairs must never collide
		assert.NotEqual(t,
			tunnelResourceName(prefix, "a-b", "c"),
			tunnelResourceName(prefix, "a", "b-c"),
		)
		// Service vs no-service must never collide
		assert.NotEqual(t,
			tunnelResourceName(prefix, "a-b", ""),
			tunnelResourceName(prefix, "a", "b"),
		)
	}

	assert.Equal(t, "diverge-tunnel-lease-my-preview", tunnelResourceName("diverge-tunnel-lease", "my-preview", ""))

	// Disjoint unnamed vs named helpers, including truncated max-length previewIDs.
	assert.Equal(t, "diverge-tunnel-my-preview", tunnelServiceName("my-preview", ""))
	assert.True(t, strings.HasPrefix(tunnelServiceName("my-preview", "api"), "diverge-tsvc-"))
	assert.NotEqual(t, tunnelServiceName("a-b", ""), tunnelServiceName("a", "b"))
	assert.NotEqual(t, tunnelServiceName(maxLabel, ""), tunnelServiceName(maxLabel, "a"))
	assert.LessOrEqual(t, len(tunnelServiceName(maxLabel, "a")), 63)

	assert.Equal(t, "diverge-tunnel-lease-my-preview", tunnelLeaseName("my-preview", ""))
	assert.True(t, strings.HasPrefix(tunnelLeaseName("my-preview", "api"), "diverge-tlease-"))
	assert.NotEqual(t, tunnelLeaseName("a-b", ""), tunnelLeaseName("a", "b"))
	assert.NotEqual(t, tunnelLeaseName(maxLabel, ""), tunnelLeaseName(maxLabel, "a"))
	assert.LessOrEqual(t, len(tunnelLeaseName(maxLabel, "a")), 63)
}

func TestValidateTunnelMetadata(t *testing.T) {
	assert.NoError(t, validateTunnelMetadata(nil))
	assert.NoError(t, validateTunnelMetadata(map[string]string{
		"kind":        "module",
		"asset.path":  "/remoteEntry.js",
		"service_tag": "v1",
	}))

	// >16 entries
	tooMany := make(map[string]string, 17)
	for i := 0; i < 17; i++ {
		tooMany[fmt.Sprintf("k%d", i)] = "v"
	}
	err := validateTunnelMetadata(tooMany)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "16 entries")

	// Invalid key characters
	require.Error(t, validateTunnelMetadata(map[string]string{"BadKey": "v"}))
	require.Error(t, validateTunnelMetadata(map[string]string{"": "v"}))

	// Control characters / invalid UTF-8
	require.Error(t, validateTunnelMetadata(map[string]string{"k": "line1\nline2"}))
	require.Error(t, validateTunnelMetadata(map[string]string{"k": "bad\x00val"}))
	require.Error(t, validateTunnelMetadata(map[string]string{"k": string([]byte{0xff, 0xfe})}))

	// > 2048 bytes JSON
	tooBig := map[string]string{"big": strings.Repeat("a", 2040)}
	err = validateTunnelMetadata(tooBig)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2048 bytes")
}
