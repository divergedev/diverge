package deployer

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// URLFetcher downloads pre-rendered YAML manifests from an HTTP endpoint.
// The URL is read from env.Spec.Deploy.Manifests.URL.
// An optional auth token can be set via the AuthToken field.
type URLFetcher struct {
	HTTPClient *http.Client
	AuthToken  string
}

func (f *URLFetcher) Fetch(ctx context.Context, env *v1alpha1.Environment) ([]unstructured.Unstructured, error) {
	if env.Spec.Deploy.Manifests == nil || env.Spec.Deploy.Manifests.URL == "" {
		return nil, fmt.Errorf("manifest URL not specified in environment %s/%s", env.Namespace, env.Name)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.Spec.Deploy.Manifests.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if f.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+f.AuthToken)
	}

	resp, err := f.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch manifests from %s: %w", env.Spec.Deploy.Manifests.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching manifests from %s", resp.StatusCode, env.Spec.Deploy.Manifests.URL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return parseMultiDocYAML(body)
}
