package deployer

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// DirectDeployer applies pre-rendered Kubernetes manifests directly
// via Server-Side Apply, without requiring ArgoCD.
type DirectDeployer struct {
	Client  client.Client
	Fetcher ManifestFetcher
}

// Deploy fetches pre-rendered manifests and applies them via Server-Side Apply.
func (d *DirectDeployer) Deploy(ctx context.Context, env *v1alpha1.Environment) error {
	logger := log.FromContext(ctx).WithName("direct-deployer")

	objects, err := d.Fetcher.Fetch(ctx, env)
	if err != nil {
		return fmt.Errorf("failed to fetch manifests: %w", err)
	}

	if len(objects) == 0 {
		logger.Info("No manifests to deploy")
		return nil
	}

	// Determine target namespace
	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	logger.Info("Deploying manifests", "count", len(objects), "namespace", targetNS)

	for i := range objects {
		obj := &objects[i]

		// Set target namespace for namespaced resources
		if obj.GetNamespace() == "" {
			obj.SetNamespace(targetNS)
		}

		// Inject Diverge labels
		existingLabels := obj.GetLabels()
		if existingLabels == nil {
			existingLabels = make(map[string]string)
		}
		existingLabels["diverge.io/environment"] = env.Name
		existingLabels["diverge.io/managed-by"] = "diverge"
		obj.SetLabels(existingLabels)

		// Set OwnerReference for 'same' namespace mode
		// (enables automatic GC when Environment CR is deleted)
		if env.Spec.Deploy.Namespace != "create" {
			ownerRef := metav1.OwnerReference{
				APIVersion: env.APIVersion,
				Kind:       env.Kind,
				Name:       env.Name,
				UID:        env.UID,
			}
			obj.SetOwnerReferences([]metav1.OwnerReference{ownerRef})
		}

		// Apply via Server-Side Apply
		patch := client.Apply
		opts := []client.PatchOption{
			client.FieldOwner("diverge-direct-deployer"),
			client.ForceOwnership,
		}
		if err := d.Client.Patch(ctx, obj, patch, opts...); err != nil {
			return fmt.Errorf("failed to apply %s %s/%s: %w",
				obj.GetKind(), obj.GetNamespace(), obj.GetName(), err)
		}

		logger.V(1).Info("Applied resource",
			"kind", obj.GetKind(),
			"name", obj.GetName(),
			"namespace", obj.GetNamespace(),
		)
	}

	logger.Info("Successfully deployed all manifests", "count", len(objects))
	return nil
}

// Teardown cleans up deployed resources.
// For 'create' namespace mode: no-op — the controller deletes the namespace.
// For 'same' namespace mode: no-op — OwnerReferences trigger automatic GC
// when the Environment CR is deleted.
func (d *DirectDeployer) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	logger := log.FromContext(ctx).WithName("direct-deployer")
	logger.Info("Teardown requested",
		"namespace_mode", env.Spec.Deploy.Namespace,
		"environment", env.Name,
	)
	// Resources are cleaned up via:
	// - 'create' mode: namespace deletion (handled by environment_controller)
	// - 'same' mode: OwnerReference GC (automatic when Environment CR is deleted)
	return nil
}

// Status returns the deployment status of resources managed by this deployer.
func (d *DirectDeployer) Status(ctx context.Context, env *v1alpha1.Environment) ([]ServiceStatus, error) {
	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	selector := labels.SelectorFromSet(map[string]string{
		"diverge.io/environment": env.Name,
		"diverge.io/managed-by":  "diverge",
	})

	var result []ServiceStatus

	// Check Deployments
	var deployList appsv1.DeploymentList
	if err := d.Client.List(ctx, &deployList,
		client.InNamespace(targetNS),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		return nil, fmt.Errorf("failed to list deployments: %w", err)
	}
	for _, dep := range deployList.Items {
		result = append(result, ServiceStatus{
			Name:       dep.Name,
			Service:    dep.Labels["diverge.io/service"],
			SyncStatus: "Applied",
			Health:     deploymentHealth(&dep),
		})
	}

	// Check StatefulSets
	var stsList appsv1.StatefulSetList
	if err := d.Client.List(ctx, &stsList,
		client.InNamespace(targetNS),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		return nil, fmt.Errorf("failed to list statefulsets: %w", err)
	}
	for _, sts := range stsList.Items {
		result = append(result, ServiceStatus{
			Name:       sts.Name,
			Service:    sts.Labels["diverge.io/service"],
			SyncStatus: "Applied",
			Health:     statefulSetHealth(&sts),
		})
	}

	return result, nil
}

// deploymentHealth determines the health of a Deployment based on its rollout status.
func deploymentHealth(dep *appsv1.Deployment) string {
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	if dep.Status.AvailableReplicas >= desired && dep.Status.UpdatedReplicas >= desired {
		return "Healthy"
	}
	if dep.Status.AvailableReplicas == 0 && dep.Generation > 1 {
		return "Degraded"
	}
	return "Progressing"
}

// statefulSetHealth determines the health of a StatefulSet.
func statefulSetHealth(sts *appsv1.StatefulSet) string {
	desired := int32(1)
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}
	if sts.Status.ReadyReplicas >= desired && sts.Status.UpdatedReplicas >= desired {
		return "Healthy"
	}
	if sts.Status.ReadyReplicas == 0 && sts.Generation > 1 {
		return "Degraded"
	}
	return "Progressing"
}
