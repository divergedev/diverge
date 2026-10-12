package proxy

import (
	"strings"
	"testing"

	"hegel.dev/go/hegel"
)

func TestExtractEnvName(t *testing.T) {
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

func sanitizeDNSLabel(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	res := strings.Trim(b.String(), "-")
	if len(res) > 63 {
		res = strings.Trim(res[:63], "-")
	}
	return res
}

func TestIsAllowedOrigin_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		raw := hegel.Draw(ht, hegel.Text())
		sub := sanitizeDNSLabel(raw)
		if sub == "" {
			return
		}

		domain := "preview.example.com"

		// Valid subdomains over http and https (with or without port) always pass
		validHTTPS := "https://" + sub + "." + domain
		if !isAllowedOrigin(validHTTPS, domain) {
			ht.Fatalf("Expected valid origin %q to be allowed", validHTTPS)
		}

		validHTTPWithPort := "http://" + sub + "." + domain + ":8080"
		if !isAllowedOrigin(validHTTPWithPort, domain) {
			ht.Fatalf("Expected valid origin with port %q to be allowed", validHTTPWithPort)
		}

		// Origins with non-root paths always fail
		withPath := validHTTPS + "/" + sub
		if isAllowedOrigin(withPath, domain) {
			ht.Fatalf("Expected origin with path %q to be rejected", withPath)
		}

		// Origins with userinfo always fail
		withUserInfo := "https://" + sub + ":secret@" + sub + "." + domain
		if isAllowedOrigin(withUserInfo, domain) {
			ht.Fatalf("Expected origin with userinfo %q to be rejected", withUserInfo)
		}

		// Origins with query or fragment always fail
		withQuery := validHTTPS + "?env=" + sub
		if isAllowedOrigin(withQuery, domain) {
			ht.Fatalf("Expected origin with query %q to be rejected", withQuery)
		}

		withFragment := validHTTPS + "#" + sub
		if isAllowedOrigin(withFragment, domain) {
			ht.Fatalf("Expected origin with fragment %q to be rejected", withFragment)
		}

		// Other domains always fail
		otherDomain := "https://" + sub + ".other.example.com"
		if isAllowedOrigin(otherDomain, domain) {
			ht.Fatalf("Expected unrelated domain %q to be rejected", otherDomain)
		}
	})
}

func TestSingleJoiningSlash_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		a := hegel.Draw(ht, hegel.Text())
		b := hegel.Draw(ht, hegel.Text())

		got := singleJoiningSlash(a, b)
		if a == "" || a == "/" {
			if got != b {
				ht.Fatalf("singleJoiningSlash(%q, %q) = %q, want %q", a, b, got, b)
			}
			return
		}
		if b == "" || b == "/" {
			if got != a {
				ht.Fatalf("singleJoiningSlash(%q, %q) = %q, want %q", a, b, got, a)
			}
			return
		}

		left := strings.TrimRight(a, "/")
		right := strings.TrimLeft(b, "/")
		want := left + "/" + right
		if got != want {
			ht.Fatalf("singleJoiningSlash(%q, %q) = %q, want %q", a, b, got, want)
		}
		if !strings.Contains(left, "//") && !strings.Contains(right, "//") && strings.Contains(got, "//") {
			ht.Fatalf("singleJoiningSlash(%q, %q) = %q produced unexpected double slash", a, b, got)
		}
	})
}
