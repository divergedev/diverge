package proxy

import (
	"testing"

	"hegel.dev/go/hegel"
)

func TestExtractEnvName(t *testing.T) {
	// Property: extractEnvName for any subdomain always returns the first segment
	// Wait, actually extractEnvName returns the whole prefix. If domain is preview.example.com, and host is mr-42.preview.example.com, it returns mr-42.
	// We'll test that if the host ends with .previewDomain, it returns the prefix, otherwise "".

	hegel.Test(t, func(ht *hegel.T) {
		envName := hegel.Draw(ht, hegel.Text())

		// filter out empty envName
		if envName == "" {
			return
		}

		previewDomain := "preview.example.com"
		host := envName + "." + previewDomain

		result := extractEnvName(host, previewDomain)
		if result != envName {
			ht.Fatalf("Expected %q, got %q", envName, result)
		}

		// Also test that invalid domain returns ""
		invalidHost := envName + ".other.domain.com"
		if extractEnvName(invalidHost, previewDomain) != "" {
			ht.Fatalf("Expected empty for invalid domain %q", invalidHost)
		}
	})
}
