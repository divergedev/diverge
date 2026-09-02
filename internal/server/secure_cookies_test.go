package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
