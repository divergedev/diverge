package deployer

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/features"
)

// ServiceConfigFetcher generates Kubernetes Deployment and Service manifests
// from the ServicePreviewConfig on the Environment CR. This is used for
// multi-repo preview mode where we deploy a single preview pod based on
// the .diverge.yaml configuration rather than fetching pre-rendered manifests.
type ServiceConfigFetcher struct{}

var _ ManifestFetcher = (*ServiceConfigFetcher)(nil)

// Fetch generates a Deployment and Service for the preview pod.
func (f *ServiceConfigFetcher) Fetch(ctx context.Context, env *v1alpha1.Environment) ([]unstructured.Unstructured, error) {
	cfg := env.Spec.ServiceConfig
	if cfg == nil {
		return nil, fmt.Errorf("serviceConfig is required for ServiceConfigFetcher")
	}
	if cfg.ServiceName == "" {
		return nil, fmt.Errorf("serviceConfig.serviceName is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("serviceConfig.port %d out of valid range 1-65535", cfg.Port)
	}
	if cfg.Image == "" {
		return nil, fmt.Errorf("serviceConfig.image is required")
	}

	previewName := fmt.Sprintf("%s-%s", env.Name, cfg.ServiceName)
	previewID := env.Name

	// Default imagePullPolicy to IfNotPresent
	pullPolicy := cfg.ImagePullPolicy
	if pullPolicy == "" {
		pullPolicy = "IfNotPresent"
	}

	// Build env vars — auto-inject APP_VERSION, then add user-provided (skipping dupes)
	seen := map[string]bool{"APP_VERSION": true}
	var containerEnv []interface{}
	containerEnv = append(containerEnv, map[string]interface{}{
		"name":  "APP_VERSION",
		"value": previewID,
	})
	for _, e := range cfg.Env {
		if seen[e.Name] {
			continue
		}
		seen[e.Name] = true
		containerEnv = append(containerEnv, map[string]interface{}{
			"name":  e.Name,
			"value": e.Value,
		})
	}

	// Build envFrom for database secret injection
	var containerEnvFrom []interface{}
	dbSecretName := resolveDBSecret(env)
	if dbSecretName != "" {
		containerEnvFrom = append(containerEnvFrom, map[string]interface{}{
			"secretRef": map[string]interface{}{
				"name": dbSecretName,
			},
		})
	}

	// If DatabaseEnvKey is set and differs from DATABASE_URL, add explicit mapping
	if dbSecretName != "" && cfg.DatabaseEnvKey != "" && !seen[cfg.DatabaseEnvKey] {
		containerEnv = append(containerEnv, map[string]interface{}{
			"name": cfg.DatabaseEnvKey,
			"valueFrom": map[string]interface{}{
				"secretKeyRef": map[string]interface{}{
					"name": dbSecretName,
					"key":  "DATABASE_URL",
				},
			},
		})
	}

	// Inject feature flag ConfigMap volume and env vars if configured
	var containerVolumeMounts []interface{}
	var podVolumes []interface{}
	if env.Spec.Features != nil {
		cmName := features.ConfigMapName(env.Name)
		if !seen["DIVERGE_FEATURE_CONFIGMAP"] {
			seen["DIVERGE_FEATURE_CONFIGMAP"] = true
			containerEnv = append(containerEnv, map[string]interface{}{
				"name":  "DIVERGE_FEATURE_CONFIGMAP",
				"value": cmName,
			})
		}
		if !seen["FLAGD_FLAG_PATH"] {
			seen["FLAGD_FLAG_PATH"] = true
			containerEnv = append(containerEnv, map[string]interface{}{
				"name":  "FLAGD_FLAG_PATH",
				"value": "/etc/diverge/flags/flags.json",
			})
		}
		containerVolumeMounts = append(containerVolumeMounts, map[string]interface{}{
			"name":      "diverge-features",
			"mountPath": "/etc/diverge/flags",
			"readOnly":  true,
		})
		podVolumes = append(podVolumes, map[string]interface{}{
			"name": "diverge-features",
			"configMap": map[string]interface{}{
				"name": cmName,
			},
		})
	}

	// Inject any feature environment variables from status
	for k, v := range env.Status.FeatureEnvVars {
		if !seen[k] {
			seen[k] = true
			containerEnv = append(containerEnv, map[string]interface{}{
				"name":  k,
				"value": v,
			})
		}
	}

	// Deployment
	deploy := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name": previewName,
				"labels": map[string]interface{}{
					"app":                        previewName,
					"divergedev.com/service":     cfg.ServiceName,
					"divergedev.com/role":        "preview",
					"divergedev.com/preview-id":  previewID,
					"divergedev.com/environment": env.Name,
					"divergedev.com/managed-by":  "diverge",
				},
			},
			"spec": map[string]interface{}{
				"replicas": int64(1),
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app":                       previewName,
						"divergedev.com/preview-id": previewID,
					},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app":                        previewName,
							"divergedev.com/service":     cfg.ServiceName,
							"divergedev.com/role":        "preview",
							"divergedev.com/preview-id":  previewID,
							"divergedev.com/environment": env.Name,
						},
					},
					"spec": map[string]interface{}{
						"automountServiceAccountToken": false,
						"containers": []interface{}{
							map[string]interface{}{
								"name":            cfg.ServiceName,
								"image":           cfg.Image,
								"imagePullPolicy": pullPolicy,
								"ports": []interface{}{
									map[string]interface{}{
										"containerPort": int64(cfg.Port),
									},
								},
								"env": containerEnv,
								"readinessProbe": map[string]interface{}{
									"httpGet": map[string]interface{}{
										"path": "/health",
										"port": int64(cfg.Port),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Conditionally inject envFrom if there are database secrets to mount
	if len(containerEnvFrom) > 0 {
		containers := deploy.Object["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})["containers"].([]interface{})
		container := containers[0].(map[string]interface{})
		container["envFrom"] = containerEnvFrom
	}
	if len(containerVolumeMounts) > 0 {
		containers := deploy.Object["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})["containers"].([]interface{})
		container := containers[0].(map[string]interface{})
		container["volumeMounts"] = containerVolumeMounts
	}
	if len(podVolumes) > 0 {
		podSpec := deploy.Object["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})
		podSpec["volumes"] = podVolumes
	}

	// Service
	svc := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name": previewName,
				"labels": map[string]interface{}{
					"divergedev.com/role":        "preview",
					"divergedev.com/preview-id":  previewID,
					"divergedev.com/environment": env.Name,
					"divergedev.com/managed-by":  "diverge",
				},
			},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{
					"app":                       previewName,
					"divergedev.com/preview-id": previewID,
				},
				"ports": []interface{}{
					map[string]interface{}{
						"port":       int64(cfg.Port),
						"targetPort": int64(cfg.Port),
					},
				},
			},
		},
	}

	return []unstructured.Unstructured{deploy, svc}, nil
}

// resolveDBSecret determines the database connection Secret name for a preview pod.
// Priority:
//  1. connectionRef (user's pre-existing Secret — the primary path)
//  2. Auto-provisioned Secret from SchemaProvider (diverge-db-<schemaName>)
//  3. Empty string (no database injection)
func resolveDBSecret(env *v1alpha1.Environment) string {
	db := env.Spec.Database

	// Primary path: user-provided connection secret
	if db.ConnectionRef != "" {
		return db.ConnectionRef
	}

	// Auto-provisioned: schema mode no longer uses secrets, it injects env vars directly
	// so we return empty here.
	return ""
}
