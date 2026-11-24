package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxTunnelMetadataEntries = 16
	maxTunnelMetadataBytes   = 2048
	dns1123LabelMaxLen       = 63
	tunnelHashSuffixLen      = 8
)

var tunnelMetadataKeyRegex = regexp.MustCompile(`^[a-z0-9._-]{1,63}$`)

// tunnelMapKey builds the in-memory lookup key for an active tunnel.
func tunnelMapKey(namespace, previewID, service string) string {
	if service != "" {
		return namespace + "/" + previewID + "/" + service
	}
	return namespace + "/" + previewID
}

// tunnelResourceName builds a valid DNS-1123 label (<= 63 chars) for per-tunnel K8s resources.
// When service is non-empty, a deterministic 8-char SHA-256 suffix derived from
// "prefix/previewID/service" is always appended so hyphenated pairs like
// ("a-b", "c") and ("a", "b-c") never collide.
func tunnelResourceName(prefix, previewID, service string) string {
	raw := prefix + "-" + previewID
	if service != "" {
		raw += "-" + service
	}
	if service == "" && len(raw) <= dns1123LabelMaxLen {
		return raw
	}

	hashInput := prefix + "/" + previewID + "/" + service
	sum := sha256.Sum256([]byte(hashInput))
	hashSuffix := hex.EncodeToString(sum[:])[:tunnelHashSuffixLen]

	maxPrefixLen := dns1123LabelMaxLen - 1 - tunnelHashSuffixLen // 54
	if len(raw) > maxPrefixLen {
		raw = raw[:maxPrefixLen]
	}
	raw = strings.Trim(raw, "-")
	if raw == "" {
		return "t-" + hashSuffix
	}
	return raw + "-" + hashSuffix
}

// tunnelServiceName builds the DNS-1123 K8s Service/EndpointSlice name for a tunnel.
// Unnamed single-port tunnels use the "diverge-tunnel-" prefix, while named-service
// tunnels use the disjoint "diverge-tsvc-" prefix with an 8-char hash suffix so named
// and unnamed resources never alias even under truncation.
func tunnelServiceName(previewID, service string) string {
	if service == "" {
		return tunnelResourceName("diverge-tunnel", previewID, "")
	}
	return tunnelResourceName("diverge-tsvc", previewID, service)
}

// tunnelLeaseName builds the DNS-1123 K8s Lease name for a tunnel.
// Unnamed single-port tunnels use the "diverge-tunnel-lease-" prefix, while named-service
// tunnels use the disjoint "diverge-tlease-" prefix with an 8-char hash suffix so named
// and unnamed Leases never alias even under truncation.
func tunnelLeaseName(previewID, service string) string {
	if service == "" {
		return tunnelResourceName("diverge-tunnel-lease", previewID, "")
	}
	return tunnelResourceName("diverge-tlease", previewID, service)
}

// validateTunnelMetadata enforces key format, entry count, UTF-8/control-char safety,
// and serialized JSON size bounds on TunnelRegister.metadata.
func validateTunnelMetadata(meta map[string]string) error {
	if len(meta) == 0 {
		return nil
	}
	if len(meta) > maxTunnelMetadataEntries {
		return fmt.Errorf("metadata exceeds maximum of %d entries", maxTunnelMetadataEntries)
	}
	for k, v := range meta {
		if !tunnelMetadataKeyRegex.MatchString(k) {
			return fmt.Errorf("invalid metadata key %q (must match ^[a-z0-9._-]{1,63}$)", k)
		}
		if !utf8.ValidString(v) {
			return fmt.Errorf("metadata value for %q must be valid UTF-8", k)
		}
		if strings.ContainsAny(v, "\x00\r\n") {
			return fmt.Errorf("metadata value for %q must not contain control characters", k)
		}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	if len(raw) > maxTunnelMetadataBytes {
		return fmt.Errorf("metadata exceeds maximum serialized size of %d bytes", maxTunnelMetadataBytes)
	}
	return nil
}

func encodeTunnelMetadata(meta map[string]string) (string, error) {
	if len(meta) == 0 {
		return "", nil
	}
	if err := validateTunnelMetadata(meta); err != nil {
		return "", err
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
