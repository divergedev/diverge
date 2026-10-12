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

func TestGitHubConfigFetcher_FetchConfig(t *testing.T) {
	ctx := context.Background()

	t.Run("200 OK returns config bytes", func(t *testing.T) {
		var gotAuth, gotAccept string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			gotAccept = r.Header.Get("Accept")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("serviceName: payments\n"))
		}))
		defer srv.Close()

		fetcher := &GitHubConfigFetcher{
			Token: "ghp_test123",
			HTTPClient: &http.Client{
				Transport: &mockTransport{
					roundTripFunc: func(req *http.Request) (*http.Response, error) {
						newReq, err := http.NewRequestWithContext(req.Context(), req.Method, srv.URL+req.URL.Path+"?"+req.URL.RawQuery, nil)
						if err != nil {
							return nil, err
						}
						newReq.Header = req.Header
						return srv.Client().Do(newReq)
					},
				},
			},
		}

		data, err := fetcher.FetchConfig(ctx, "github", "org/repo", "main")
		require.NoError(t, err)
		assert.Equal(t, "serviceName: payments\n", string(data))
		assert.Equal(t, "Bearer ghp_test123", gotAuth)
		assert.Equal(t, "application/vnd.github.raw+json", gotAccept)
	})

	t.Run("404 returns ErrConfigNotFound", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		fetcher := &GitHubConfigFetcher{
			Token: "ghp_test123",
			HTTPClient: &http.Client{
				Transport: &mockTransport{
					roundTripFunc: func(req *http.Request) (*http.Response, error) {
						newReq, _ := http.NewRequestWithContext(req.Context(), req.Method, srv.URL, nil)
						return srv.Client().Do(newReq)
					},
				},
			},
		}

		_, err := fetcher.FetchConfig(ctx, "github", "org/repo", "main")
		assert.ErrorIs(t, err, config.ErrConfigNotFound)
	})

	t.Run("500 returns unexpected status error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		fetcher := &GitHubConfigFetcher{
			Token: "ghp_test123",
			HTTPClient: &http.Client{
				Transport: &mockTransport{
					roundTripFunc: func(req *http.Request) (*http.Response, error) {
						newReq, _ := http.NewRequestWithContext(req.Context(), req.Method, srv.URL, nil)
						return srv.Client().Do(newReq)
					},
				},
			},
		}

		_, err := fetcher.FetchConfig(ctx, "github", "org/repo", "main")
		require.ErrorContains(t, err, "unexpected status 500")
	})

	t.Run("oversized body is rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("a", (1<<20)+10)))
		}))
		defer srv.Close()

		fetcher := &GitHubConfigFetcher{
			Token: "ghp_test123",
			HTTPClient: &http.Client{
				Transport: &mockTransport{
					roundTripFunc: func(req *http.Request) (*http.Response, error) {
						newReq, _ := http.NewRequestWithContext(req.Context(), req.Method, srv.URL, nil)
						return srv.Client().Do(newReq)
					},
				},
			},
		}

		_, err := fetcher.FetchConfig(ctx, "github", "org/repo", "main")
		require.ErrorContains(t, err, "exceeds maximum size")
	})
}
