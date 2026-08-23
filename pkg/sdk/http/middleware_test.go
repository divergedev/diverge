package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divergedev/diverge/pkg/sdk"
)

func TestPropagateEnvironment_HeaderPassthrough(t *testing.T) {
	handler := PropagateEnvironment(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := sdk.EnvironmentFromContext(r.Context())
		assert.Equal(t, "pr-123", env)
		assert.Equal(t, "pr-123", r.Header.Get(DefaultHeaderKey))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(DefaultHeaderKey, "pr-123")
	handler.ServeHTTP(httptest.NewRecorder(), req)
}

func TestPropagateEnvironmentWithOptions_Precedence(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		query      string
		host       string
		baseDomain string
		wantEnv    string
	}{
		{
			name:    "header wins over query and subdomain",
			header:  "from-header",
			query:   "from-query",
			host:    "from-sub.preview.example.com",
			wantEnv: "from-header",
		},
		{
			name:    "query wins over subdomain",
			query:   "from-query",
			host:    "from-sub.preview.example.com",
			wantEnv: "from-query",
		},
		{
			name:       "subdomain fallback",
			host:       "pr-42.preview.example.com",
			baseDomain: "preview.example.com",
			wantEnv:    "pr-42",
		},
		{
			name:    "no match returns empty",
			host:    "example.com",
			wantEnv: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := []Option{
				WithQueryParams("x-diverge-env"),
			}
			if tt.baseDomain != "" {
				opts = append(opts, WithBaseDomain(tt.baseDomain))
			} else {
				opts = append(opts, WithBaseDomain("preview.example.com"))
			}

			var gotEnv string
			handler := PropagateEnvironmentWithOptions(opts...)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotEnv = sdk.EnvironmentFromContext(r.Context())
			}))

			url := "http://example.com/"
			if tt.query != "" {
				url += "?x-diverge-env=" + tt.query
			}
			req := httptest.NewRequest("GET", url, nil)
			if tt.header != "" {
				req.Header.Set(DefaultHeaderKey, tt.header)
			}
			if tt.host != "" {
				req.Host = tt.host
			}
			handler.ServeHTTP(httptest.NewRecorder(), req)

			assert.Equal(t, tt.wantEnv, gotEnv)
		})
	}
}

func TestExtractSubdomain(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		baseDomain string
		wantEnv    string
	}{
		{"basic", "pr-123.preview.example.com", "preview.example.com", "pr-123"},
		{"with port", "pr-123.preview.example.com:8080", "preview.example.com", "pr-123"},
		{"case insensitive", "PR-123.Preview.Example.Com", "preview.example.com", "pr-123"},
		{"root domain no match", "preview.example.com", "preview.example.com", ""},
		{"partial prefix no match", "evil-preview.example.com", "preview.example.com", ""},
		{"no match", "other.example.com", "preview.example.com", ""},
		{"invalid chars", "env!@#.preview.example.com", "preview.example.com", ""},
		{"too long", "a123456789012345678901234567890123456789012345678901234567890123456789.preview.example.com", "preview.example.com", ""},
		{"valid hyphenated", "my-feature-branch.preview.example.com", "preview.example.com", "my-feature-branch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSubdomain(tt.host, tt.baseDomain)
			assert.Equal(t, tt.wantEnv, got)
		})
	}
}

func TestQueryParamExtraction(t *testing.T) {
	opts := []Option{
		WithQueryParams("x-diverge-env", "diverge_env"),
	}

	var gotEnv string
	handler := PropagateEnvironmentWithOptions(opts...)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEnv = sdk.EnvironmentFromContext(r.Context())
	}))

	t.Run("first key match", func(t *testing.T) {
		gotEnv = ""
		req := httptest.NewRequest("GET", "/?x-diverge-env=pr-1", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
		assert.Equal(t, "pr-1", gotEnv)
	})

	t.Run("second key fallback", func(t *testing.T) {
		gotEnv = ""
		req := httptest.NewRequest("GET", "/?diverge_env=pr-2", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
		assert.Equal(t, "pr-2", gotEnv)
	})

	t.Run("invalid value rejected", func(t *testing.T) {
		gotEnv = ""
		req := httptest.NewRequest("GET", "/?x-diverge-env=INVALID%0D%0AX-Injected:+evil", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
		assert.Empty(t, gotEnv, "CRLF injection should be rejected by RFC 1123 validation")
	})
}

func TestStripQueryParam(t *testing.T) {
	opts := []Option{
		WithQueryParams("x-diverge-env"),
		WithStripQueryParam(true),
	}

	var gotURL string
	handler := PropagateEnvironmentWithOptions(opts...)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
	}))

	req := httptest.NewRequest("GET", "/?x-diverge-env=pr-5&other=keep", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.NotContains(t, gotURL, "x-diverge-env")
	assert.Contains(t, gotURL, "other=keep")
}

func TestHeaderPromotedOnExtraction(t *testing.T) {
	opts := []Option{
		WithBaseDomain("preview.example.com"),
	}

	var gotHeader string
	handler := PropagateEnvironmentWithOptions(opts...)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(DefaultHeaderKey)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "pr-99.preview.example.com"
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "pr-99", gotHeader, "extracted env should be promoted to header")
}

func TestIsValidEnvName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"simple", "pr-123", true},
		{"lowercase", "my-feature", true},
		{"numbers only", "123", true},
		{"single char", "a", true},
		{"starts with hyphen", "-invalid", false},
		{"ends with hyphen", "invalid-", false},
		{"uppercase", "PR-123", false},
		{"spaces", "pr 123", false},
		{"crlf injection", "pr\r\nX-Evil: bad", false},
		{"empty", "", false},
		{"63 chars (max)", "a23456789012345678901234567890123456789012345678901234567890123", true},
		{"64 chars (over)", "a234567890123456789012345678901234567890123456789012345678901234", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, isValidEnvName(tt.input))
		})
	}
}

func TestRoundTripper_InjectsHeader(t *testing.T) {
	var gotHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(DefaultHeaderKey)
	}))
	defer server.Close()

	ctx := sdk.WithEnvironment(t.Context(), "pr-42")
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	require.NoError(t, err)

	client := &http.Client{Transport: RoundTripper(nil)}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, "pr-42", gotHeader)
}

func TestBackwardCompatibility(t *testing.T) {
	// PropagateEnvironment (no options) should still work exactly as before
	var gotEnv string
	handler := PropagateEnvironment(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEnv = sdk.EnvironmentFromContext(r.Context())
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(DefaultHeaderKey, "legacy-env")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "legacy-env", gotEnv)
}
