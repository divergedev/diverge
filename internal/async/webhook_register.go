package async

import (
	"flag"
	"fmt"

	"github.com/divergedev/diverge/pkg/registry"
)

var asyncWebhookEndpoint string

func init() {
	flag.StringVar(&asyncWebhookEndpoint, "async-webhook-endpoint", "", "Endpoint URL for the async provisioning webhook")

	Providers.Register("webhook", registry.Provider[Provisioner]{
		Create: func(deps registry.Deps) (Provisioner, error) {
			if asyncWebhookEndpoint == "" {
				return nil, fmt.Errorf("--async-webhook-endpoint is required for webhook provisioner")
			}
			return NewWebhookProvisioner(asyncWebhookEndpoint), nil
		},
		Description: "HTTP webhook-based async provisioner",
	})
}
