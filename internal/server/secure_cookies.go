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

// CookiePolicy defines the security settings for HTTP cookies issued by the server.
type CookiePolicy struct {
	Secure bool
}

// CookiePolicyResolver resolves the effective CookiePolicy based on operator configuration
// and environment signals (such as local TLS termination or public redirect URLs).
type CookiePolicyResolver struct {
	Mode       string // "auto", "true", "false"
	TLSEnabled bool
	PublicURL  string // Generalized public URL (e.g. OIDC redirect URL or external base URL)
}

// Resolve evaluates the policy according to the configured mode and environmental signals.
//
// In auto mode the server sets Secure when it terminates TLS itself, and also
// when the public URL (e.g. OIDC redirect URL) is https. The latter covers
// deployments where an ingress or gateway terminates TLS and forwards cleartext,
// which the server would otherwise read as plain HTTP and issue a non-Secure cookie.
func (r CookiePolicyResolver) Resolve() (CookiePolicy, error) {
	switch strings.ToLower(strings.TrimSpace(r.Mode)) {
	case SecureCookiesAlways:
		return CookiePolicy{Secure: true}, nil
	case SecureCookiesDisabled:
		return CookiePolicy{Secure: false}, nil
	case "", SecureCookiesAuto:
		if r.TLSEnabled {
			return CookiePolicy{Secure: true}, nil
		}
		return CookiePolicy{Secure: isHTTPS(r.PublicURL)}, nil
	default:
		return CookiePolicy{}, fmt.Errorf("invalid --secure-cookies value %q: must be %q, %q or %q",
			r.Mode, SecureCookiesAuto, SecureCookiesAlways, SecureCookiesDisabled)
	}
}

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
	policy, err := CookiePolicyResolver{
		Mode:       mode,
		TLSEnabled: tlsEnabled,
		PublicURL:  oidcRedirectURL,
	}.Resolve()
	if err != nil {
		return false, err
	}
	return policy.Secure, nil
}

func isHTTPS(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "https")
}
