package deployer

import (
	"flag"

	"github.com/divergedev/diverge/internal/argocd"
	"github.com/divergedev/diverge/pkg/registry"
)

var (
	argoNamespace = flag.String("argo-namespace", "argocd", "Namespace where Argo CD is installed")
	argoRepoURL   = flag.String("argo-repo-url", "", "Repository URL for Argo CD Application sources")
)

func init() {
	Providers.Register("argocd", registry.Provider[Deployer]{
		Create: func(deps registry.Deps) (Deployer, error) {
			client := argocd.NewClient(deps.Client, *argoNamespace)
			gen := &argocd.Generator{
				ArgoNamespace:     *argoNamespace,
				RepoURL:           *argoRepoURL,
				DestinationServer: "https://kubernetes.default.svc",
				Project:           "default",
			}
			return NewArgoDeployer(client, gen, nil), nil
		},
		Description: "Argo CD Application deployment",
	})
}
