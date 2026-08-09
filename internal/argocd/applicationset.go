package argocd

import (
	"context"
	"fmt"

	"github.com/divergedev/diverge/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type ApplicationSetGenerator struct {
	ArgoNamespace string // namespace where Argo CD is installed
	RepoURL       string // Helm chart repository URL
}

// GenerateApplicationSet creates an ApplicationSet for the given environment.
// It uses the list generator with the changed services.
func (g *ApplicationSetGenerator) GenerateApplicationSet(
	ctx context.Context,
	env *v1alpha1.Environment,
	changedServices []string,
	serviceConfigs map[string]ServiceConfig,
) (*unstructured.Unstructured, error) {

	elements := make([]map[string]interface{}, 0, len(changedServices))
	for _, svcName := range changedServices {
		cfg, ok := serviceConfigs[svcName]
		if !ok {
			return nil, fmt.Errorf("service config not found for %s", svcName)
		}
		
		elements = append(elements, map[string]interface{}{
			"service":   cfg.Name,
			"image":     cfg.Tag,
			"chart":     cfg.ChartPath,
			"namespace": env.Name,
		})
	}

	appSet := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "ApplicationSet",
			"metadata": map[string]interface{}{
				"name":      fmt.Sprintf("diverge-%s", env.Name),
				"namespace": g.ArgoNamespace,
				"labels": map[string]interface{}{
					"diverge.io/environment": env.Name,
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
				"generators": []interface{}{
					map[string]interface{}{
						"list": map[string]interface{}{
							"elements": elements,
						},
					},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": fmt.Sprintf("diverge-%s-{{service}}", env.Name),
						"labels": map[string]interface{}{
							"diverge.io/environment": env.Name,
						},
					},
					"spec": map[string]interface{}{
						"project": "default",
						"source": map[string]interface{}{
							// Argo CD needs repoURL for helm sources. If chart path is used, repoURL might be required.
							// Usually people put a git URL or helm repo URL here. We'll leave it simple.
							"repoURL": g.RepoURL,
							"path":    "{{chart}}",
						},
						"destination": map[string]interface{}{
							"server":    "https://kubernetes.default.svc",
							"namespace": "{{namespace}}",
						},
						"syncPolicy": map[string]interface{}{
							"automated": map[string]interface{}{
								"prune":    true,
								"selfHeal": true,
							},
						},
					},
				},
			},
		},
	}

	return appSet, nil
}
