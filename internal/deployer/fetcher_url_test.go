package deployer

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsDisallowedIP(t *testing.T) {
	tests := []struct {
		name       string
		ip         string
		disallowed bool
	}{
		{name: "loopback v4", ip: "127.0.0.1", disallowed: true},
		{name: "loopback v6", ip: "::1", disallowed: true},
		{name: "private 10.x", ip: "10.0.0.1", disallowed: true},
		{name: "private 172.16.x", ip: "172.16.0.1", disallowed: true},
		{name: "private 192.168.x", ip: "192.168.1.1", disallowed: true},
		{name: "link-local metadata", ip: "169.254.169.254", disallowed: true},
		{name: "unspecified v4", ip: "0.0.0.0", disallowed: true},
		{name: "unspecified v6", ip: "::", disallowed: true},
		{name: "multicast v4", ip: "224.0.0.1", disallowed: true},
		{name: "public IP", ip: "93.184.216.34", disallowed: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := isDisallowedIP(net.ParseIP(tc.ip))
			if tc.disallowed {
				assert.Error(t, err, "expected IP %s to be disallowed", tc.ip)
			} else {
				assert.NoError(t, err, "expected IP %s to be allowed", tc.ip)
			}
		})
	}
	assert.Error(t, isDisallowedIP(nil))
}

func TestValidateManifestURL_SSRFProtection(t *testing.T) {
	assert.ErrorContains(t, validateManifestURL("http://example.com/manifests.yaml"), "scheme must be https")
	assert.ErrorContains(t, validateManifestURL("https://127.0.0.1/manifests.yaml"), "loopback")
	assert.ErrorContains(t, validateManifestURL("https://10.1.2.3/manifests.yaml"), "private")
	assert.ErrorContains(t, validateManifestURL("https://169.254.169.254/latest/meta-data"), "metadata")
	assert.ErrorContains(t, validateManifestURL("https://0.0.0.0/manifests.yaml"), "unspecified")
	assert.ErrorContains(t, validateManifestURL("https://224.0.0.1/manifests.yaml"), "multicast")
}

func TestNewURLFetcher_BlocksDialToDisallowedIP(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fetcher := NewURLFetcher("token-123")
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "env-1", Namespace: "default"},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Manifests: &v1alpha1.ManifestSource{
					URL: srv.URL,
				},
			},
		},
	}

	// Default validation blocks loopback HTTPS server
	_, err := fetcher.Fetch(context.Background(), env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loopback")

	// Even if SkipURLValidation is true, the DNS-pinning DialContext on NewURLFetcher's transport blocks loopback
	fetcher.SkipURLValidation = true
	_, err = fetcher.Fetch(context.Background(), env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loopback")
}

func TestURLFetcher_Fetch_SuccessWithSkipValidation(t *testing.T) {
	yamlBody := "apiVersion: v1\nkind: Service\nmetadata:\n  name: my-svc\n---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: my-dep\n"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(yamlBody))
	}))
	defer srv.Close()

	fetcher := &URLFetcher{
		HTTPClient:        srv.Client(),
		AuthToken:         "my-secret-token",
		SkipURLValidation: true,
	}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "env-1", Namespace: "default"},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Manifests: &v1alpha1.ManifestSource{
					URL: srv.URL,
				},
			},
		},
	}

	objs, err := fetcher.Fetch(context.Background(), env)
	require.NoError(t, err)
	require.Len(t, objs, 2)
	assert.Equal(t, "Bearer my-secret-token", gotAuth)
	assert.Equal(t, "Service", objs[0].GetKind())
	assert.Equal(t, "Deployment", objs[1].GetKind())
}

func TestURLFetcher_Fetch_MissingManifestURL(t *testing.T) {
	fetcher := NewURLFetcher("")
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "env-1", Namespace: "default"},
	}
	_, err := fetcher.Fetch(context.Background(), env)
	require.ErrorContains(t, err, "manifest URL not specified")
}
