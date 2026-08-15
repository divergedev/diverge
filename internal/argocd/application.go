package argocd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	"github.com/divergedev/diverge/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var deniedNamespaces = map[string]bool{
	"kube-system": true, "kube-public": true, "kube-node-lease": true,
	"default": true, "argocd": true,
}

var rfc1123Re = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func sanitizeName(name string, maxLen int) string {
	if len(name) <= maxLen {
		return name
	}
	hash := sha256.Sum256([]byte(name))
	hashStr := hex.EncodeToString(hash[:])[:8]
	return name[:maxLen-9] + "-" + hashStr
}

// Generator creates Argo CD Application resources for environments.
// Instead of creating one ApplicationSet per environment (anti-pattern),
// it creates individual Application CRs per changed service. The Diverge
// operator stays in control of the full lifecycle: delta deployments,
// database provisioning, routing, and TTLs.
type Generator struct {
	ArgoNamespace     string // namespace where Argo CD is installed
	RepoURL           string // Helm chart repository URL
	DestinationServer string // Destination server URL
	Project           string // Argo CD Project
}

// Generate creates one Argo CD Application per changed service.
// Each Application is owned by the Environment CR for cascade deletion.
func (g *Generator) Generate(
	env *v1alpha1.Environment,
	changedServices []string,
	serviceConfigs map[string]ServiceConfig,
) ([]*unstructured.Unstructured, error) {

	if env == nil {
		return nil, errors.New("environment must not be nil")
	}

	var destNamespace string
	if env.Spec.Deploy.Namespace == "create" {
		destNamespace = env.PreviewNamespace()
	} else {
		// "same" mode (default): deploy in the CR's own namespace
		destNamespace = env.Namespace
		if destNamespace == "" {
			destNamespace = "default"
		}
	}

	if deniedNamespaces[destNamespace] {
		return nil, fmt.Errorf("destination namespace %q is forbidden", destNamespace)
	}

	destServer := g.DestinationServer
	if destServer == "" {
		destServer = "https://kubernetes.default.svc"
	}

	project := g.Project
	if project == "" {
		project = "default"
	}

	apps := make([]*unstructured.Unstructured, 0, len(changedServices))

	for _, svcName := range changedServices {
		cfg, ok := serviceConfigs[svcName]
		if !ok {
			return nil, fmt.Errorf("service config not found for %q; verify it is defined in your diverge.yaml", svcName)
		}

		if !rfc1123Re.MatchString(cfg.Name) {
			return nil, fmt.Errorf("service name %q must conform to RFC 1123", cfg.Name)
		}

		appName := fmt.Sprintf("diverge-%s-%s-%s", env.Namespace, env.Name, cfg.Name)
		appName = sanitizeName(appName, 253)

		envNameLabel := sanitizeName(env.Name, 63)
		svcNameLabel := sanitizeName(cfg.Name, 63)
		envNamespaceLabel := sanitizeName(env.Namespace, 63)

		targetRevision := env.Spec.Source.Branch
		if targetRevision == "" {
			targetRevision = "HEAD"
		}

		app := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "argoproj.io/v1alpha1",
				"kind":       "Application",
				"metadata": map[string]interface{}{
					"name":      appName,
					"namespace": g.ArgoNamespace,
					"labels": map[string]interface{}{
						"diverge.io/environment":           envNameLabel,
						"diverge.io/environment-namespace": envNamespaceLabel,
						"diverge.io/service":               svcNameLabel,
						"diverge.io/managed-by":            "diverge",
					},
					"annotations": map[string]interface{}{
						"diverge.io/environment-namespace": env.Namespace,
						"diverge.io/source-branch":         env.Spec.Source.Branch,
						"diverge.io/source-mr":             fmt.Sprintf("%d", env.Spec.Source.MR),
					},
					"finalizers": []interface{}{
						"resources-finalizer.argocd.argoproj.io",
					},
				},
				"spec": map[string]interface{}{
					"project": project,
					"source":  g.buildSource(cfg, targetRevision),
					"destination": map[string]interface{}{
						"server":    destServer,
						"namespace": destNamespace,
					},
					"syncPolicy": map[string]interface{}{
						"automated": map[string]interface{}{
							"prune":    true,
							"selfHeal": true,
						},
						"syncOptions": []interface{}{
							"CreateNamespace=true",
							"ServerSideApply=true",
							"IgnoreExtraneous=true",
						},
					},
					"ignoreDifferences": []interface{}{
						map[string]interface{}{
							"group":        "gateway.networking.k8s.io",
							"kind":         "HTTPRoute",
							"jsonPointers": []interface{}{"/spec/rules"},
						},
						map[string]interface{}{
							"group":             "discovery.k8s.io",
							"kind":              "EndpointSlice",
							"jqPathExpressions": []interface{}{".endpoints", ".ports"},
						},
						map[string]interface{}{
							"group":        "apps",
							"kind":         "Deployment",
							"jsonPointers": []interface{}{"/spec/replicas"},
						},
					},
				},
			},
		}

		apps = append(apps, app)
	}

	return apps, nil
}

// buildSource creates the Argo CD Application source block based on the
// service's source type. Defaults to Helm if SourceType is empty.
func (g *Generator) buildSource(cfg ServiceConfig, targetRevision string) map[string]interface{} {
	switch cfg.SourceType {
	case "kustomize":
		source := map[string]interface{}{
			"repoURL":        g.RepoURL,
			"path":           cfg.Path,
			"targetRevision": targetRevision,
		}
		// Kustomize image override: sets the container image for the preview
		if cfg.Image != "" && cfg.Tag != "" {
			source["kustomize"] = map[string]interface{}{
				"images": []interface{}{
					fmt.Sprintf("%s:%s", cfg.Image, cfg.Tag),
				},
			}
		} else if cfg.Tag != "" {
			// Image name not specified, use service name as default
			source["kustomize"] = map[string]interface{}{
				"images": []interface{}{
					fmt.Sprintf("%s:%s", cfg.Name, cfg.Tag),
				},
			}
		}
		return source

	default: // "helm" or empty
		return map[string]interface{}{
			"repoURL":        g.RepoURL,
			"path":           cfg.ChartPath,
			"targetRevision": targetRevision,
			"helm": map[string]interface{}{
				"parameters": []interface{}{
					map[string]interface{}{
						"name":  "image.tag",
						"value": cfg.Tag,
					},
				},
			},
		}
	}
}
