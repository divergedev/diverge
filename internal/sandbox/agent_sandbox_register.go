//go:build !no_sandbox

package sandbox

import (
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/divergedev/diverge/pkg/registry"
	pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

func init() {
	pkgsandbox.Providers.Register("agent-sandbox", registry.Provider[pkgsandbox.SandboxProvider]{
		Create: func(deps registry.Deps) (pkgsandbox.SandboxProvider, error) {
			cs := deps.Clientset
			if cs == nil {
				if cfg, err := ctrl.GetConfig(); err == nil && cfg != nil {
					if c, err := kubernetes.NewForConfig(cfg); err == nil {
						cs = c
					}
				}
			}
			return NewAgentSandboxProvider(deps.Client, cs, deps.Scheme), nil
		},
		Description: "Kubernetes Agent Sandbox (SIG Apps) provider",
	})
}
