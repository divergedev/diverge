//go:build !no_sandbox

package sandbox

import (
	"k8s.io/client-go/kubernetes"

	"github.com/divergedev/diverge/pkg/registry"
	pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

func init() {
	pkgsandbox.Providers.Register("agent-sandbox", registry.Provider[pkgsandbox.SandboxProvider]{
		Create: func(deps registry.Deps) (pkgsandbox.SandboxProvider, error) {
			var cs kubernetes.Interface
			// Create a clientset if possible or pass nil
			return NewAgentSandboxProvider(deps.Client, cs, deps.Scheme), nil
		},
		Description: "Kubernetes Agent Sandbox (SIG Apps) provider",
	})
}
