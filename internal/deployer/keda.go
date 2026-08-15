package deployer

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/divergedev/diverge/api/v1alpha1"
)

var hsoGVK = schema.GroupVersionKind{
	Group:   "http.keda.sh",
	Version: "v1alpha1",
	Kind:    "HTTPScaledObject",
}

var hsoListGVK = schema.GroupVersionKind{
	Group:   "http.keda.sh",
	Version: "v1alpha1",
	Kind:    "HTTPScaledObjectList",
}

// KEDAConfig represents the configuration or state for this type.
type KEDAConfig struct {
	// TODO: move to CRD PreviewGroupServiceSpec in v2.0
	MinReplicas int64
	MaxReplicas int64
	Cooldown    int64
}

// KEDADeployer represents the configuration or state for this type.
type KEDADeployer struct {
	Inner  Deployer
	Client client.Client
	Config KEDAConfig
}

// Deploy performs its designated operation.
func (d *KEDADeployer) Deploy(ctx context.Context, env *v1alpha1.Environment) error {
	logger := log.FromContext(ctx).WithName("keda-deployer")

	if err := d.Inner.Deploy(ctx, env); err != nil {
		return err
	}

	// Check if KEDA CRD exists
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(hsoListGVK)
	if err := d.Client.List(ctx, list, client.Limit(1)); err != nil {
		if meta.IsNoMatchError(err) {
			logger.V(1).Info("KEDA HTTPScaledObject CRD not found, skipping KEDA integration")
			return nil
		}
		return fmt.Errorf("failed to list HTTPScaledObjects: %w", err)
	}

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	targetName := env.Name
	targetPort := int64(80)
	if env.Spec.ServiceConfig != nil {
		if env.Spec.ServiceConfig.ServiceName != "" {
			targetName = fmt.Sprintf("%s-%s", env.Name, env.Spec.ServiceConfig.ServiceName)
		}
		if env.Spec.ServiceConfig.Port != 0 {
			targetPort = int64(env.Spec.ServiceConfig.Port)
		}
	}

	hso := &unstructured.Unstructured{}
	hso.SetGroupVersionKind(hsoGVK)
	hso.SetName(targetName)
	hso.SetNamespace(targetNS)
	hso.SetLabels(map[string]string{
		"diverge.io/managed-by":  "diverge",
		"diverge.io/environment": env.Name,
	})

	if err := unstructured.SetNestedField(hso.Object, targetName, "spec", "scaleTargetRef", "name"); err != nil {
		return fmt.Errorf("failed to set scaleTargetRef.name: %w", err)
	}
	if err := unstructured.SetNestedField(hso.Object, targetName, "spec", "scaleTargetRef", "service"); err != nil {
		return fmt.Errorf("failed to set scaleTargetRef.service: %w", err)
	}
	if err := unstructured.SetNestedField(hso.Object, targetPort, "spec", "scaleTargetRef", "port"); err != nil {
		return fmt.Errorf("failed to set scaleTargetRef.port: %w", err)
	}
	minRepl := d.Config.MinReplicas
	maxRepl := d.Config.MaxReplicas
	if maxRepl == 0 {
		maxRepl = 3
	}
	cooldown := d.Config.Cooldown
	if cooldown == 0 {
		cooldown = 300
	}
	if err := unstructured.SetNestedField(hso.Object, minRepl, "spec", "replicas", "min"); err != nil {
		return fmt.Errorf("failed to set replicas.min: %w", err)
	}
	if err := unstructured.SetNestedField(hso.Object, maxRepl, "spec", "replicas", "max"); err != nil {
		return fmt.Errorf("failed to set replicas.max: %w", err)
	}
	if err := unstructured.SetNestedField(hso.Object, cooldown, "spec", "scaledownPeriod"); err != nil {
		return fmt.Errorf("failed to set scaledownPeriod: %w", err)
	}

	// Apply via Server-Side Apply (create-or-patch)
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(hsoGVK)
	err := d.Client.Get(ctx, client.ObjectKey{Name: targetName, Namespace: targetNS}, existing)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to check existing HTTPScaledObject: %w", err)
		}
		// Resource doesn't exist, create it
		if err := d.Client.Create(ctx, hso); err != nil {
			return fmt.Errorf("failed to create HTTPScaledObject: %w", err)
		}
	} else {
		// Resource exists, patch via SSA
		opts := []client.ApplyOption{
			client.FieldOwner("diverge-keda-deployer"),
			client.ForceOwnership,
		}
		if err := d.Client.Apply(ctx, client.ApplyConfigurationFromUnstructured(hso), opts...); err != nil {
			return fmt.Errorf("failed to apply HTTPScaledObject: %w", err)
		}
	}

	logger.Info("Successfully deployed HTTPScaledObject", "name", targetName, "namespace", targetNS)
	return nil
}

// Teardown performs its designated operation.
func (d *KEDADeployer) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	logger := log.FromContext(ctx).WithName("keda-deployer")

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	targetName := env.Name
	if env.Spec.ServiceConfig != nil && env.Spec.ServiceConfig.ServiceName != "" {
		targetName = fmt.Sprintf("%s-%s", env.Name, env.Spec.ServiceConfig.ServiceName)
	}

	hso := &unstructured.Unstructured{}
	hso.SetGroupVersionKind(hsoGVK)
	hso.SetName(targetName)
	hso.SetNamespace(targetNS)

	if err := d.Client.Delete(ctx, hso); err != nil {
		if !meta.IsNoMatchError(err) && client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete HTTPScaledObject: %w", err)
		}
		logger.V(1).Info("HTTPScaledObject CRD missing or resource not found during teardown")
	}

	return d.Inner.Teardown(ctx, env)
}

// Status performs its designated operation.
func (d *KEDADeployer) Status(ctx context.Context, env *v1alpha1.Environment) ([]ServiceStatus, error) {
	innerStatus, err := d.Inner.Status(ctx, env)
	if err != nil {
		return nil, err
	}

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	targetName := env.Name
	if env.Spec.ServiceConfig != nil && env.Spec.ServiceConfig.ServiceName != "" {
		targetName = fmt.Sprintf("%s-%s", env.Name, env.Spec.ServiceConfig.ServiceName)
	}

	hso := &unstructured.Unstructured{}
	hso.SetGroupVersionKind(hsoGVK)
	err = d.Client.Get(ctx, client.ObjectKey{Name: targetName, Namespace: targetNS}, hso)
	if err != nil {
		if meta.IsNoMatchError(err) || client.IgnoreNotFound(err) == nil {
			// HSO doesn't exist or CRD doesn't exist, we just return inner status
			return innerStatus, nil
		}
		return nil, fmt.Errorf("failed to get HTTPScaledObject: %w", err)
	}

	// Aggregate status from both Inner deployer and HTTPScaledObject.
	// If HSO exists, append its status.
	hsoReady := isHSOReady(hso)
	hsoHealth := "Progressing"
	if hsoReady {
		hsoHealth = "Healthy"
	}

	innerStatus = append(innerStatus, ServiceStatus{
		Name:       env.Name,
		Service:    "http-scaled-object",
		SyncStatus: "Applied",
		Health:     hsoHealth,
	})

	return innerStatus, nil
}

func isHSOReady(hso *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(hso.Object, "status", "conditions")
	if !found || err != nil {
		return false
	}
	for _, cond := range conditions {
		if c, ok := cond.(map[string]interface{}); ok {
			if c["type"] == "Ready" && c["status"] == "True" {
				return true
			}
		}
	}
	return false
}
