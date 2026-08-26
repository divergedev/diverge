package controller

import (
	"context"
	"fmt"
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/database"
)

// ensureAtlasCR creates or updates an AtlasMigration or AtlasSchema CR.
func (r *PreviewGroupReconciler) ensureAtlasCR(ctx context.Context, env *divergeiov1alpha1.Environment, dbResult *database.DatabaseResult) error {
	logger := log.FromContext(ctx)
	atlasSpec := env.Spec.Database.Atlas

	if atlasSpec == nil {
		return nil
	}

	if dbResult == nil || dbResult.DSN == "" {
		return fmt.Errorf("atlas migration requires a provisioned database with DSN")
	}

	// Create DSN Secret
	secretName, err := createDSNSecret(ctx, r.Client, env.Name, env.Namespace, dbResult.DSN, env)
	if err != nil {
		return fmt.Errorf("failed to create DSN secret for atlas: %w", err)
	}

	kind := "AtlasMigration"
	if atlasSpec.Mode == "declarative" {
		kind = "AtlasSchema"
	}

	crName := generateHookJobName(env.Name, "atlas")

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("db.atlasgo.io/v1alpha1")
	u.SetKind(kind)
	u.SetName(crName)
	u.SetNamespace(env.Namespace)

	// Set owner reference
	t := true
	u.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion:         "diverge.io/v1alpha1",
			Kind:               "Environment",
			Name:               env.Name,
			UID:                env.UID,
			Controller:         &t,
			BlockOwnerDeletion: &t,
		},
	})

	spec := map[string]interface{}{
		"urlFrom": map[string]interface{}{
			"secretKeyRef": map[string]interface{}{
				"name": secretName,
				"key":  "url",
			},
		},
	}

	// Set dir or schema based on type
	if kind == "AtlasMigration" {
		if atlasSpec.MigrationConfigMap != "" {
			spec["dir"] = map[string]interface{}{
				"configMapRef": map[string]interface{}{
					"name": atlasSpec.MigrationConfigMap,
				},
			}
		}
	} else {
		if atlasSpec.SchemaConfigMap != "" {
			spec["schema"] = map[string]interface{}{
				"configMapRef": map[string]interface{}{
					"name": atlasSpec.SchemaConfigMap,
				},
			}
		}
	}

	if atlasSpec.Policy != nil {
		if atlasSpec.Policy.Destructive != "" {
			spec["policy"] = map[string]interface{}{
				"destructive": atlasSpec.Policy.Destructive,
			}
		}
	}

	err = unstructured.SetNestedMap(u.Object, spec, "spec")
	if err != nil {
		return fmt.Errorf("failed to set unstructured spec: %w", err)
	}

	// Create or update
	var existing unstructured.Unstructured
	existing.SetAPIVersion("db.atlasgo.io/v1alpha1")
	existing.SetKind(kind)
	err = r.Get(ctx, types.NamespacedName{Name: crName, Namespace: env.Namespace}, &existing)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get %s: %w", kind, err)
		}
		logger.Info(fmt.Sprintf("Creating %s", kind), "name", crName)
		if err := r.Create(ctx, u); err != nil {
			return fmt.Errorf("failed to create %s: %w", kind, err)
		}
		// Treat as newly created, so wait if blocking
		existing = *u
	} else {
		existingSpec, _, _ := unstructured.NestedMap(existing.Object, "spec")
		uSpec, _, _ := unstructured.NestedMap(u.Object, "spec")
		if !reflect.DeepEqual(existingSpec, uSpec) {
			logger.Info(fmt.Sprintf("Updating %s", kind), "name", crName)
			err = unstructured.SetNestedMap(existing.Object, uSpec, "spec")
			if err != nil {
				return fmt.Errorf("failed to set unstructured spec for update: %w", err)
			}
			if err := r.Update(ctx, &existing); err != nil {
				return fmt.Errorf("failed to update %s: %w", kind, err)
			}
		}
	}

	isBlocking := true
	if atlasSpec.Blocking != nil {
		isBlocking = *atlasSpec.Blocking
	}

	if !isBlocking {
		return nil
	}

	// Check status
	// In Atlas operator, status.conditions contains type Ready.
	conditions, found, err := unstructured.NestedSlice(existing.Object, "status", "conditions")
	if err != nil {
		return fmt.Errorf("failed to parse %s status conditions: %w", kind, err)
	}
	if found {
		for _, c := range conditions {
			cond, ok := c.(map[string]interface{})
			if ok {
				ctype, _ := cond["type"].(string)
				cstatus, _ := cond["status"].(string)
				if ctype == "Ready" {
					if cstatus == "True" {
						logger.Info(fmt.Sprintf("%s ready", kind), "name", crName)
						return nil
					}
					// If it's ready false, wait or fail based on reason. We'll wait.
					creason, _ := cond["reason"].(string)
					if creason == "Failed" {
						return fmt.Errorf("%s %s failed", kind, crName)
					}
				}
			}
		}
	}

	return fmt.Errorf("%s %s is still running", kind, crName)
}
