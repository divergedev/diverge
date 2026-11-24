package tunnelclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitPOSIXCommand(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr string
	}{
		{
			name:  "simple command and flags",
			input: "oauth-cli token --aud diverge",
			want:  []string{"oauth-cli", "token", "--aud", "diverge"},
		},
		{
			name:  "single and double quotes with spaces",
			input: `my-cmd --msg 'hello world' --other "two  spaces"`,
			want:  []string{"my-cmd", "--msg", "hello world", "--other", "two  spaces"},
		},
		{
			name:  "escaped quotes and spaces",
			input: `my-cmd foo\ bar "a\"b\\c\$d\` + "`" + `e\n"`,
			want:  []string{"my-cmd", "foo bar", `a"b\c$d` + "`" + `e\n`},
		},
		{
			name:  "explicit empty string arg",
			input: `my-cmd "" ''`,
			want:  []string{"my-cmd", "", ""},
		},
		{
			name:  "metacharacters preserved inside single quotes",
			input: `printf '%s' 'a;b|c&d$e'`,
			want:  []string{"printf", "%s", "a;b|c&d$e"},
		},
		{
			name:    "empty input",
			input:   "   \t  ",
			wantErr: "must not be empty",
		},
		{
			name:    "unterminated single quote",
			input:   `cmd 'unterminated`,
			wantErr: "unterminated quote",
		},
		{
			name:    "unterminated double quote",
			input:   `cmd "unterminated`,
			wantErr: "unterminated quote",
		},
		{
			name:    "trailing backslash",
			input:   `cmd trailing\`,
			wantErr: "unterminated escape",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SplitPOSIXCommand(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	// Reject every unquoted shell metacharacter explicitly.
	metachars := []string{";", "|", "&", "<", ">", "`", "$", "(", ")", "\n", "\r"}
	for _, mc := range metachars {
		t.Run("rejects_metachar_"+mc, func(t *testing.T) {
			_, err := SplitPOSIXCommand("echo a" + mc + "b")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "without a shell")
		})
	}
}

func TestCommandTokenSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test helper relies on sh")
	}

	t.Run("executes uncached, trims whitespace, singleflights concurrent calls", func(t *testing.T) {
		tmpDir := t.TempDir()
		countFile := filepath.Join(tmpDir, "count.txt")
		script := filepath.Join(tmpDir, "tok.sh")
		err := os.WriteFile(script, []byte(fmt.Sprintf(`#!/bin/sh
echo x >> %s
sleep 0.15
printf "  tok-$(wc -l < %s | tr -d ' ')  \n"
`, countFile, countFile)), 0o700)
		require.NoError(t, err)

		ts, err := NewCommandTokenSource(script)
		require.NoError(t, err)

		// Concurrent calls coalesce via singleflight into 1 execution.
		const workers = 6
		results := make([]string, workers)
		errs := make([]error, workers)
		var wg sync.WaitGroup
		wg.Add(workers)
		for i := range workers {
			go func(idx int) {
				defer wg.Done()
				results[idx], errs[idx] = ts.Token(context.Background())
			}(i)
		}
		wg.Wait()

		for i := range workers {
			require.NoError(t, errs[i])
			assert.Equal(t, "tok-1", results[i])
		}

		// Subsequent call is uncached and runs again.
		tok2, err := ts.Token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "tok-2", tok2)

		// Invalidate is safe to call.
		ts.Invalidate()
	})

	t.Run("rejects multi-line output", func(t *testing.T) {
		ts, err := NewCommandTokenSource(`printf 'line1\nline2'`)
		require.NoError(t, err)
		_, err = ts.Token(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "newline")
	})

	t.Run("rejects empty output", func(t *testing.T) {
		ts, err := NewCommandTokenSource(`printf '   \t  '`)
		require.NoError(t, err)
		_, err = ts.Token(context.Background())
		require.ErrorIs(t, err, ErrNoCredential)
	})

	t.Run("enforces stdout size cap", func(t *testing.T) {
		ts, err := NewCommandTokenSource(`printf '0123456789abcdef'`)
		require.NoError(t, err)
		ts.maxStdout = 8
		_, err = ts.Token(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeded maximum size")
		assert.NotContains(t, err.Error(), "0123456789")
	})

	t.Run("surfaces stderr on failure", func(t *testing.T) {
		script := filepath.Join(t.TempDir(), "fail.sh")
		require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho 'auth expired' >&2\nexit 2\n"), 0o700))

		ts, err := NewCommandTokenSource(script)
		require.NoError(t, err)
		_, err = ts.Token(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "auth expired")
	})

	t.Run("detached timeout cancels stuck command without affecting caller independence", func(t *testing.T) {
		ts, err := NewCommandTokenSource(`sleep 5`)
		require.NoError(t, err)
		ts.timeout = 80 * time.Millisecond

		_, err = ts.Token(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "timed out")
	})
}

func TestFileAndChainTokenSource_Invalidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok")
	require.NoError(t, os.WriteFile(path, []byte("tok-v1\n"), 0o600))

	fts := NewFileTokenSource(path)
	chain := NewChainTokenSource(StaticTokenSource(""), fts)

	tok, err := chain.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "tok-v1", tok)

	// Rotate file immediately without waiting 5s stat cache window.
	require.NoError(t, os.WriteFile(path, []byte("tok-v2\n"), 0o600))
	tokCached, err := chain.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "tok-v1", tokCached)

	chain.Invalidate()
	tokRefreshed, err := chain.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "tok-v2", tokRefreshed)
}

func TestLoadOrCreateClientInstanceIDAt(t *testing.T) {
	dir := t.TempDir()
	idPath := filepath.Join(dir, "cfg", "diverge", "client-id")

	first := LoadOrCreateClientInstanceIDAt(idPath)
	require.NotEmpty(t, first)

	second := LoadOrCreateClientInstanceIDAt(idPath)
	assert.Equal(t, first, second)

	dirInfo, err := os.Stat(filepath.Dir(idPath))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())

	fileInfo, err := os.Stat(idPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())

	// Unwritable path falls back to ephemeral UUID without error.
	blockingFile := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(blockingFile, []byte("x"), 0o600))
	ephemeral := LoadOrCreateClientInstanceIDAt(filepath.Join(blockingFile, "client-id"))
	assert.NotEmpty(t, ephemeral)
}

func TestAuthTransportAndNewHTTPClient(t *testing.T) {
	var authCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://evil.example.com/steal", http.StatusFound)
			return
		}
		if r.Header.Get("Authorization") == "Bearer test-tok" {
			authCount.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, StaticTokenSource("test-tok"), http.DefaultTransport)

	resp, err := client.Get(srv.URL + "/ok")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, int32(1), authCount.Load())

	_, err = client.Get(srv.URL + "/redirect")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to send credentials across redirects")

	// Reject non-loopback HTTP cleartext credentials.
	req, err := http.NewRequest(http.MethodGet, "http://external.example.com/api", nil)
	require.NoError(t, err)
	_, err = client.Transport.RoundTrip(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insecure HTTP scheme is only allowed for loopback addresses")

	assert.True(t, IsLoopbackHost("127.0.0.1:8080"))
	assert.True(t, IsLoopbackHost("localhost"))
	assert.True(t, IsLoopbackHost("[::1]:3000"))
	assert.False(t, IsLoopbackHost("169.254.169.254"))
	assert.False(t, IsLoopbackHost("example.com:443"))
}

func TestIsLoopbackAddress(t *testing.T) {
	assert.True(t, IsLoopbackAddress("127.0.0.1"))
	assert.True(t, IsLoopbackAddress("127.0.0.2"))
	assert.True(t, IsLoopbackAddress("localhost"))
	assert.True(t, IsLoopbackAddress("LOCALHOST"))
	assert.True(t, IsLoopbackAddress("::1"))
	assert.True(t, IsLoopbackAddress("[::1]"))
	assert.False(t, IsLoopbackAddress(""))
	assert.False(t, IsLoopbackAddress("127.0.0.1:9999"))
	assert.False(t, IsLoopbackAddress("[::1]:3000"))
	assert.False(t, IsLoopbackAddress("localhost:8080"))
	assert.False(t, IsLoopbackAddress("169.254.169.254"))
	assert.False(t, IsLoopbackAddress("example.com"))
}

func TestAuthTransport_InsecureHTTPError(t *testing.T) {
	client := NewHTTPClient("http://example.com", StaticTokenSource("secret-tok"), http.DefaultTransport)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/ok", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInsecureHTTP)
}

func TestNewHTTPClient_BaseTransportDefaultAndRedirectCap(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer h2c-tok", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n < 15 {
			http.Redirect(w, r, fmt.Sprintf("/loop?n=%d", n+1), http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.Protocols = p
	srv.Start()
	defer srv.Close()

	// nil baseTransport must default to BaseTransport(serverAddr) (h2c for http://).
	client := NewHTTPClient(srv.URL, StaticTokenSource("h2c-tok"), nil)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/ok", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Same-origin redirects are bounded by a 10-redirect limit.
	loopReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/loop?n=0", nil)
	require.NoError(t, err)
	loopResp, err := client.Do(loopReq)
	if loopResp != nil {
		_ = loopResp.Body.Close()
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopped after 10 redirects")
}
