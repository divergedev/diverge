package server

import (
	"fmt"
	"net/url"
	"strings"
)

// Secure cookie modes for the --secure-cookies flag.
const (
	SecureCookiesAuto     = "auto"
	SecureCookiesAlways   = "true"
	SecureCookiesDisabled = "false"
)

// ResolveSecureCookies decides whether session cookies get the Secure flag.
//
// In auto mode the server is Secure when it terminates TLS itself, and also
// when the OIDC redirect URL — the public address a browser is sent back to —
// is https. The latter is what covers the common deployment where an ingress
// or gateway terminates TLS and forwards cleartext, which the server would
// otherwise read as plain HTTP and issue a non-Secure cookie for an
// HTTPS-only site.
//
// The redirect URL is used rather than a per-request X-Forwarded-Proto header
// because it is operator-supplied configuration rather than caller-controlled
// input, so it cannot be spoofed by a client.
func ResolveSecureCookies(mode string, tlsEnabled bool, oidcRedirectURL string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case SecureCookiesAlways:
		return true, nil
	case SecureCookiesDisabled:
		return false, nil
	case "", SecureCookiesAuto:
		if tlsEnabled {
			return true, nil
		}
		return redirectURLIsHTTPS(oidcRedirectURL), nil
	default:
		return false, fmt.Errorf("invalid --secure-cookies value %q: must be %q, %q or %q",
			mode, SecureCookiesAuto, SecureCookiesAlways, SecureCookiesDisabled)
	}
}

func redirectURLIsHTTPS(redirectURL string) bool {
	redirectURL = strings.TrimSpace(redirectURL)
	if redirectURL == "" {
		return false
	}
	u, err := url.Parse(redirectURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "https")
}
