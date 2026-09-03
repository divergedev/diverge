package server

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestResolveSecureCookies(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		tlsEnabled  bool
		redirectURL string
		want        bool
		wantErr     bool
	}{
		{
			// The reported defect: the chart never sets --tls-cert-file, so an
			// HTTPS-only site behind a TLS-terminating ingress issued a
			// non-Secure session cookie.
			name:        "auto: TLS terminated upstream, https redirect URL",
			mode:        SecureCookiesAuto,
			tlsEnabled:  false,
			redirectURL: "https://diverge.example.com/auth/callback",
			want:        true,
		},
		{
			name:       "auto: server terminates TLS itself",
			mode:       SecureCookiesAuto,
			tlsEnabled: true,
			want:       true,
		},
		{
			name:        "auto: plain http deployment stays insecure",
			mode:        SecureCookiesAuto,
			redirectURL: "http://diverge.internal/auth/callback",
			want:        false,
		},
		{
			name: "auto: no TLS and no redirect URL",
			mode: SecureCookiesAuto,
			want: false,
		},
		{
			name: "empty mode behaves as auto",
			mode: "",
			want: false,
		},
		{
			name:        "explicit true overrides plain http",
			mode:        SecureCookiesAlways,
			redirectURL: "http://diverge.internal/auth/callback",
			want:        true,
		},
		{
			name:       "explicit false overrides TLS",
			mode:       SecureCookiesDisabled,
			tlsEnabled: true,
			want:       false,
		},
		{
			name:        "scheme match is case-insensitive",
			mode:        SecureCookiesAuto,
			redirectURL: "HTTPS://diverge.example.com/auth/callback",
			want:        true,
		},
		{
			name:        "mode is case-insensitive and trimmed",
			mode:        "  TRUE  ",
			redirectURL: "http://diverge.internal/auth/callback",
			want:        true,
		},
		{
			name:        "malformed redirect URL is not treated as https",
			mode:        SecureCookiesAuto,
			redirectURL: "://not a url",
			want:        false,
		},
		{
			name:    "invalid mode is rejected",
			mode:    "yes",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveSecureCookies(tt.mode, tt.tlsEnabled, tt.redirectURL)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestResolveSecureCookies_ChartDefaults reproduces the shipped chart's
// configuration: server.auth.oidc.redirectUrl is https and --tls-cert-file is
// never passed. The old rule (secure := tlsCertFile != "") yielded false here.
func TestResolveSecureCookies_ChartDefaults(t *testing.T) {
	const chartRedirectURL = "https://diverge.example.com/auth/callback"
	const chartTLSCertFile = "" // the chart never sets --tls-cert-file

	oldBehaviour := chartTLSCertFile != ""
	require.False(t, oldBehaviour, "precondition: the old rule produced a non-Secure cookie")

	got, err := ResolveSecureCookies(SecureCookiesAuto, chartTLSCertFile != "", chartRedirectURL)
	require.NoError(t, err)
	assert.True(t, got, "an https-only deployment must get the Secure flag")
}

func TestResolveSecureCookies_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Invariant 1: Explicit "true" with arbitrary casing/whitespace always yields Secure=true
		trueCaseVariations := []string{"true", "True", "TRUE", "tRuE"}
		baseTrue := rapid.SampledFrom(trueCaseVariations).Draw(t, "baseTrue")
		ws1 := rapid.StringMatching(`\s{0,3}`).Draw(t, "ws1")
		ws2 := rapid.StringMatching(`\s{0,3}`).Draw(t, "ws2")
		trueMode := ws1 + baseTrue + ws2

		tlsFlag := rapid.Bool().Draw(t, "tlsFlag")
		randURL := rapid.String().Draw(t, "randURL")

		res, err := ResolveSecureCookies(trueMode, tlsFlag, randURL)
		require.NoError(t, err)
		assert.True(t, res, "explicit true mode must always return true")

		// Invariant 2: Explicit "false" with arbitrary casing/whitespace always yields Secure=false
		falseCaseVariations := []string{"false", "False", "FALSE", "fAlSe"}
		baseFalse := rapid.SampledFrom(falseCaseVariations).Draw(t, "baseFalse")
		falseMode := ws1 + baseFalse + ws2

		res, err = ResolveSecureCookies(falseMode, tlsFlag, randURL)
		require.NoError(t, err)
		assert.False(t, res, "explicit false mode must always return false")

		// Invariant 3: In auto mode, when TLS is enabled locally, always Secure=true
		autoVariations := []string{"auto", "Auto", "AUTO", ""}
		autoMode := rapid.SampledFrom(autoVariations).Draw(t, "autoMode")

		res, err = ResolveSecureCookies(autoMode, true, randURL)
		require.NoError(t, err)
		assert.True(t, res, "auto mode with local TLS must always return true")

		// Invariant 4: In auto mode without local TLS, scheme must be case-insensitive https
		scheme := rapid.SampledFrom([]string{"https", "HTTPS", "Https", "http", "HTTP", "ftp", ""}).Draw(t, "scheme")
		host := rapid.StringMatching(`[a-z0-9\-\.]{1,15}`).Draw(t, "host")
		path := rapid.StringMatching(`(/[a-z0-9]{0,5})?`).Draw(t, "path")

		var constructedURL string
		if scheme != "" {
			constructedURL = scheme + "://" + host + path
		} else {
			constructedURL = host + path
		}

		res, err = ResolveSecureCookies(autoMode, false, constructedURL)
		require.NoError(t, err)
		if strings.EqualFold(scheme, "https") {
			assert.True(t, res, "https redirect URL must yield secure=true in auto mode")
		} else {
			assert.False(t, res, "non-https redirect URL must yield secure=false in auto mode")
		}
	})
}
