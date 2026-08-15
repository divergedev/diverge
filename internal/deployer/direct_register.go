package deployer

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/divergedev/diverge/pkg/registry"
)

var (
	manifestSourceType = flag.String("manifest-source-type", "configmap", "Manifest source type for direct deployer (configmap|url|serviceconfig)")
)

func init() {
	Providers.Register("direct", registry.Provider[Deployer]{
		Create: func(deps registry.Deps) (Deployer, error) {
			var fetcher ManifestFetcher
			switch *manifestSourceType {
			case "url":
				manifestToken := os.Getenv("DIVERGE_MANIFEST_TOKEN")
				fetcher = &URLFetcher{
					HTTPClient: &http.Client{Timeout: 60 * time.Second},
					AuthToken:  manifestToken,
				}
			case "serviceconfig":
				fetcher = &ServiceConfigFetcher{}
			case "configmap", "":
				fetcher = &ConfigMapFetcher{Client: deps.Client}
			default:
				return nil, fmt.Errorf("unsupported manifest source type: %q", *manifestSourceType)
			}
			return &DirectDeployer{
				Client:  deps.Client,
				Fetcher: fetcher,
			}, nil
		},
		Description: "Direct deployment (configmap, url, or serviceconfig)",
	})
}
