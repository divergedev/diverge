package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/divergedev/diverge/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitLabConfigFetcher_FetchConfig(t *testing.T) {
	ctx := context.Background()

	t.Run("200 OK returns config bytes", func(t *testing.T) {
		var gotToken string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotToken = r.Header.Get("PRIVATE-TOKEN")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("serviceName: billing\n"))
		}))
		defer srv.Close()

		fetcher := &GitLabConfigFetcher{
			BaseURL:    srv.URL,
			Token:      "glpat-123",
			HTTPClient: srv.Client(),
		}

		data, err := fetcher.FetchConfig(ctx, "gitlab", "group/project", "main")
		require.NoError(t, err)
		assert.Equal(t, "serviceName: billing\n", string(data))
		assert.Equal(t, "glpat-123", gotToken)
	})

	t.Run("404 returns ErrConfigNotFound", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		fetcher := &GitLabConfigFetcher{
			BaseURL:    srv.URL,
			Token:      "glpat-123",
			HTTPClient: srv.Client(),
		}

		_, err := fetcher.FetchConfig(ctx, "gitlab", "group/project", "main")
		assert.ErrorIs(t, err, config.ErrConfigNotFound)
	})

	t.Run("500 returns unexpected status error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		fetcher := &GitLabConfigFetcher{
			BaseURL:    srv.URL,
			Token:      "glpat-123",
			HTTPClient: srv.Client(),
		}

		_, err := fetcher.FetchConfig(ctx, "gitlab", "group/project", "main")
		require.ErrorContains(t, err, "unexpected status 500")
	})

	t.Run("oversized body is rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+10)))
		}))
		defer srv.Close()

		fetcher := &GitLabConfigFetcher{
			BaseURL:    srv.URL,
			Token:      "glpat-123",
			HTTPClient: srv.Client(),
		}

		_, err := fetcher.FetchConfig(ctx, "gitlab", "group/project", "main")
		require.ErrorContains(t, err, "exceeds maximum size")
	})
}
