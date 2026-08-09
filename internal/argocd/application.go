package argocd

import (
	"fmt"

	"github.com/divergedev/diverge/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ApplicationGenerator creates Argo CD Application resources for environments.
// Instead of creating one ApplicationSet per environment (anti-pattern),
// it creates individual Application CRs per changed service. The Diverge
// operator stays in control of the full lifecycle: delta deployments,
// database provisioning, routing, and TTLs.
type ApplicationGenerator struct {
	ArgoNamespace string // namespace where Argo CD is installed
	RepoURL       string // Helm chart repository URL
}

// GenerateApplications creates one Argo CD Application per changed service.
// Each Application is owned by the Environment CR for cascade deletion.
func (g *ApplicationGenerator) GenerateApplications(
	env *v1alpha1.Environment,
	changedServices []string,
	serviceConfigs map[string]ServiceConfig,
) ([]*unstructured.Unstructured, error) {

	apps := make([]*unstructured.Unstructured, 0, len(changedServices))

	for _, svcName := range changedServices {
		cfg, ok := serviceConfigs[svcName]
		if !ok {
			return nil, fmt.Errorf("service config not found for %s", svcName)
		}

		app := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "argoproj.io/v1alpha1",
				"kind":       "Application",
				"metadata": map[string]interface{}{
					"name":      fmt.Sprintf("diverge-%s-%s", env.Name, cfg.Name),
					"namespace": g.ArgoNamespace,
					"labels": map[string]interface{}{
						"diverge.io/environment": env.Name,
						"diverge.io/service":     cfg.Name,
						"diverge.io/managed-by":  "diverge",
					},
					"ownerReferences": []interface{}{
						map[string]interface{}{
							"apiVersion":         env.APIVersion,
							"kind":               env.Kind,
							"name":               env.Name,
							"uid":                string(env.UID),
							"controller":         true,
							"blockOwnerDeletion": true,
						},
					},
				},
				"spec": map[string]interface{}{
					"project": "default",
					"source": map[string]interface{}{
						"repoURL":        g.RepoURL,
						"path":           cfg.ChartPath,
						"targetRevision": "HEAD",
						"helm": map[string]interface{}{
							"parameters": []interface{}{
								map[string]interface{}{
									"name":  "image.tag",
									"value": cfg.Tag,
								},
							},
						},
					},
					"destination": map[string]interface{}{
						"server":    "https://kubernetes.default.svc",
						"namespace": env.Name,
					},
					"syncPolicy": map[string]interface{}{
						"automated": map[string]interface{}{
							"prune":    true,
							"selfHeal": true,
						},
						"syncOptions": []interface{}{
							"CreateNamespace=true",
						},
					},
				},
			},
		}

		apps = append(apps, app)
	}

	return apps, nil
}
