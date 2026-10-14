package cnpg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgdb "github.com/divergedev/diverge/pkg/database"
)

const (
	DefaultStorageSize = "1Gi"
	DefaultInstances   = int64(1)
)

// ClusterGVK is the GroupVersionKind for CloudNativePG Cluster resources.
var ClusterGVK = schema.GroupVersionKind{
	Group:   "postgresql.cnpg.io",
	Version: "v1",
	Kind:    "Cluster",
}

// CNPGDatabaseProvider provisions and tears down ephemeral CloudNativePG Clusters.
type CNPGDatabaseProvider struct {
	Client          client.Client
	Scheme          *runtime.Scheme
	StorageSize     string // default "1Gi"
	Instances       int64  // default 1
	SourceCluster   string // optional default source cluster for recovery/clone
	SourceNamespace string // optional default namespace for source cluster
}

var _ pkgdb.DatabaseProvider = (*CNPGDatabaseProvider)(nil)

var nonDNSChars = regexp.MustCompile(`[^a-z0-9-]`)
var multiHyphens = regexp.MustCompile(`-{2,}`)

// ClusterName returns a valid DNS-1123 name diverge-pg-<envName> (<= 50 chars so <clusterName>-rw fits in 63 chars).
func ClusterName(envName string) string {
	clean := strings.ToLower(strings.TrimSpace(envName))
	clean = strings.NewReplacer(".", "-", "_", "-").Replace(clean)
	clean = nonDNSChars.ReplaceAllString(clean, "")
	clean = multiHyphens.ReplaceAllString(clean, "-")
	clean = strings.Trim(clean, "-")
	if clean == "" {
		clean = "env"
	}

	const prefix = "diverge-pg-"
	const maxLen = 50
	full := prefix + clean
	if len(full) <= maxLen {
		return full
	}

	sum := sha256.Sum256([]byte(envName))
	suffix := hex.EncodeToString(sum[:4]) // 8 hex chars
	truncLen := maxLen - len(prefix) - 1 - len(suffix)
	truncated := strings.TrimRight(clean[:truncLen], "-")
	return prefix + truncated + "-" + suffix
}

func targetNamespace(env *v1alpha1.Environment) string {
	if env.Spec.Deploy.Namespace == "create" {
		return env.PreviewNamespace()
	}
	if env.Namespace != "" {
		return env.Namespace
	}
	return "default"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// Provision creates or reconciles a CloudNativePG Cluster for the preview environment.
func (p *CNPGDatabaseProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*pkgdb.DatabaseResult, error) {
	if env == nil {
		return nil, fmt.Errorf("environment must not be nil")
	}
	if p.Client == nil {
		return nil, fmt.Errorf("kubernetes client is required for CNPG database provider")
	}

	clusterName := ClusterName(env.Name)
	targetNS := targetNamespace(env)
	baseNS := env.Namespace
	if baseNS == "" {
		baseNS = "default"
	}

	instances := p.Instances
	if instances <= 0 {
		instances = DefaultInstances
	}
	storageSize := firstNonEmpty(
		env.Annotations["divergedev.com/cnpg-storage-size"],
		p.StorageSize,
		DefaultStorageSize,
	)

	source := firstNonEmpty(
		env.Annotations["divergedev.com/cnpg-source-cluster"],
		env.Spec.Database.SeedSource,
		p.SourceCluster,
	)
	sourceNS := firstNonEmpty(
		env.Annotations["divergedev.com/cnpg-source-namespace"],
		p.SourceNamespace,
		baseNS,
	)

	spec := map[string]any{
		"instances": instances,
		"storage": map[string]any{
			"size": storageSize,
		},
	}

	if source != "" && env.Spec.Database.Mode != "fresh" {
		bootstrapMode := strings.TrimSpace(env.Annotations["divergedev.com/cnpg-bootstrap-mode"])
		if bootstrapMode == "recovery" {
			barmanDest := strings.TrimSpace(env.Annotations["divergedev.com/cnpg-barman-destination-path"])
			if barmanDest == "" {
				return nil, fmt.Errorf("cnpg recovery bootstrap mode requires annotation divergedev.com/cnpg-barman-destination-path")
			}
			spec["bootstrap"] = map[string]any{
				"recovery": map[string]any{
					"source": source,
				},
			}
			spec["externalClusters"] = []any{
				map[string]any{
					"name": source,
					"barmanObjectStore": map[string]any{
						"destinationPath": barmanDest,
					},
				},
			}
		} else {
			spec["bootstrap"] = map[string]any{
				"pg_basebackup": map[string]any{
					"source":   source,
					"database": "app",
					"owner":    "app",
				},
			}
			secretName := firstNonEmpty(
				env.Annotations["divergedev.com/cnpg-source-secret"],
				source+"-superuser",
			)
			secretKey := firstNonEmpty(
				env.Annotations["divergedev.com/cnpg-source-secret-key"],
				"password",
			)
			spec["externalClusters"] = []any{
				map[string]any{
					"name": source,
					"connectionParameters": map[string]any{
						"host":   fmt.Sprintf("%s-rw.%s.svc", source, sourceNS),
						"user":   "postgres",
						"dbname": "app",
					},
					"password": map[string]any{
						"name": secretName,
						"key":  secretKey,
					},
				},
			}
		}
	} else {
		spec["bootstrap"] = map[string]any{
			"initdb": map[string]any{
				"database": "app",
				"owner":    "app",
			},
		}
	}

	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(ClusterGVK)
	cluster.SetName(clusterName)
	cluster.SetNamespace(targetNS)
	cluster.SetLabels(map[string]string{
		"divergedev.com/environment":   env.Name,
		"app.kubernetes.io/managed-by": "diverge",
	})
	cluster.Object["spec"] = spec

	if targetNS == env.Namespace && p.Scheme != nil {
		if err := controllerutil.SetControllerReference(env, cluster, p.Scheme); err != nil {
			return nil, fmt.Errorf("failed to set owner reference on CNPG Cluster: %w", err)
		}
	}

	if err := p.Client.Create(ctx, cluster); err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("failed to create CNPG Cluster %s/%s: %w", targetNS, clusterName, err)
	}

	status, err := p.Status(ctx, env)
	if err != nil {
		return nil, err
	}

	dsn := fmt.Sprintf("postgres://app@%s-rw.%s.svc:5432/app?sslmode=disable", clusterName, targetNS)
	hasSecretURI := false
	var appSecret corev1.Secret
	if err := p.Client.Get(ctx, types.NamespacedName{
		Name:      clusterName + "-app",
		Namespace: targetNS,
	}, &appSecret); err == nil {
		if uri := strings.TrimSpace(string(appSecret.Data["uri"])); uri != "" {
			dsn = uri
			hasSecretURI = true
		}
	}

	ready := status.Provisioned && hasSecretURI
	msg := status.Message
	if status.Provisioned && !hasSecretURI {
		msg = fmt.Sprintf("Waiting for CNPG credentials secret %s/%s", targetNS, clusterName+"-app")
	}

	return &pkgdb.DatabaseResult{
		DSN:      dsn,
		AdminDSN: dsn,
		EnvVars: map[string]string{
			"DATABASE_URL":         dsn,
			"DIVERGE_CNPG_CLUSTER": clusterName,
		},
		Ready:   ready,
		Message: msg,
	}, nil
}

// Status inspects the CNPG Cluster status phase and conditions.
func (p *CNPGDatabaseProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*pkgdb.DatabaseStatus, error) {
	if env == nil {
		return nil, fmt.Errorf("environment must not be nil")
	}
	if p.Client == nil {
		return nil, fmt.Errorf("kubernetes client is required for CNPG database provider")
	}

	clusterName := ClusterName(env.Name)
	targetNS := targetNamespace(env)

	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(ClusterGVK)
	err := p.Client.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: targetNS}, cluster)
	if err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return &pkgdb.DatabaseStatus{
				Provisioned: false,
				SchemaName:  clusterName,
				Message:     fmt.Sprintf("CNPG cluster %s not found", clusterName),
			}, nil
		}
		return nil, fmt.Errorf("failed to get CNPG Cluster %s/%s: %w", targetNS, clusterName, err)
	}

	phase, _, _ := unstructured.NestedString(cluster.Object, "status", "phase")
	ready := phase == "Cluster in healthy state"

	if !ready {
		conditions, _, _ := unstructured.NestedSlice(cluster.Object, "status", "conditions")
		for _, raw := range conditions {
			cond, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			cType, _ := cond["type"].(string)
			cStatus, _ := cond["status"].(string)
			if cType == "Ready" && cStatus == string(metav1.ConditionTrue) {
				ready = true
				break
			}
		}
	}

	msg := fmt.Sprintf("CNPG cluster %s is provisioning", clusterName)
	if phase != "" {
		msg = fmt.Sprintf("CNPG cluster %s: %s", clusterName, phase)
	} else if ready {
		msg = fmt.Sprintf("CNPG cluster %s is ready", clusterName)
	}

	return &pkgdb.DatabaseStatus{
		Provisioned: ready,
		SchemaName:  clusterName,
		Message:     msg,
	}, nil
}

// Teardown deletes the CNPG Cluster for the preview environment idempotently.
func (p *CNPGDatabaseProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	if env == nil || p.Client == nil {
		return nil
	}

	clusterName := ClusterName(env.Name)
	targetNS := targetNamespace(env)

	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(ClusterGVK)
	cluster.SetName(clusterName)
	cluster.SetNamespace(targetNS)

	if err := p.Client.Delete(ctx, cluster); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("failed to delete CNPG Cluster %s/%s: %w", targetNS, clusterName, err)
	}
	return nil
}
