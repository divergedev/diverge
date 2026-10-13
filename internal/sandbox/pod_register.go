//go:build !no_sandbox

package sandbox

import (
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/divergedev/diverge/pkg/registry"
	pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

func init() {
	factory := func(deps registry.Deps) (pkgsandbox.SandboxProvider, error) {
		cs := deps.Clientset
		if cs == nil {
			if cfg, err := ctrl.GetConfig(); err == nil && cfg != nil {
				if c, err := kubernetes.NewForConfig(cfg); err == nil {
					cs = c
				}
			}
		}
		return NewPodSandboxProvider(deps.Client, cs, deps.Scheme), nil
	}
	pkgsandbox.Providers.Register("pod", registry.Provider[pkgsandbox.SandboxProvider]{
		Create:      factory,
		Description: "Native Kubernetes Pod sandbox provider",
	})
	pkgsandbox.Providers.Register("k8s-pod", registry.Provider[pkgsandbox.SandboxProvider]{
		Create:      factory,
		Description: "Native Kubernetes Pod sandbox provider (alias for pod)",
	})
}
