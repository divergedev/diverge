package deployer

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

		// CR2: Enforce namespace scope — reject or override any namespace
		// that doesn't match targetNS to prevent cross-namespace writes.
		objNS := obj.GetNamespace()
		if objNS != "" && objNS != targetNS {
			return fmt.Errorf("manifest %s %s/%s targets namespace %q, but environment targets %q; cross-namespace manifests are not allowed",
				obj.GetKind(), objNS, obj.GetName(), objNS, targetNS)
		}
		obj.SetNamespace(targetNS)

		// Inject Diverge labels
		existingLabels := obj.GetLabels()
		if existingLabels == nil {
			existingLabels = make(map[string]string)
		}
		existingLabels["diverge.io/environment"] = env.Name
		existingLabels["diverge.io/managed-by"] = "diverge"
		obj.SetLabels(existingLabels)

		// Inject OTel annotations if present
		for k, v := range env.Annotations {
			if strings.HasPrefix(k, "instrumentation.opentelemetry.io/") {
				// Inject into Pod template if applicable
				kind := obj.GetKind()
				if kind == "Deployment" || kind == "StatefulSet" || kind == "DaemonSet" || kind == "Job" {
					annos, found, _ := unstructured.NestedStringMap(obj.Object, "spec", "template", "metadata", "annotations")
					if !found || annos == nil {
						annos = make(map[string]string)
					}
					annos[k] = v
					_ = unstructured.SetNestedStringMap(obj.Object, annos, "spec", "template", "metadata", "annotations")
				}
			}
		}

		// Set OwnerReference for 'same' namespace mode only.
		// CR2: Only set OwnerReferences when the object is in the same
		// namespace as the Environment CR (required by K8s GC).
		// In 'create' mode, namespace deletion handles cleanup.
		if env.Spec.Deploy.Namespace != "create" && obj.GetNamespace() == env.Namespace {
			ownerRef := metav1.OwnerReference{
				APIVersion: env.APIVersion,
				Kind:       env.Kind,
				Name:       env.Name,
				UID:        env.UID,
			}
			obj.SetOwnerReferences([]metav1.OwnerReference{ownerRef})
		}

		// Apply via Server-Side Apply (create-or-patch)
		existing := obj.DeepCopy()
		err = d.Client.Get(ctx, client.ObjectKeyFromObject(obj), existing)
		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to check existing %s %s/%s: %w",
					obj.GetKind(), obj.GetNamespace(), obj.GetName(), err)
			}
			// Resource doesn't exist, create it
			if err := d.Client.Create(ctx, obj); err != nil {
				return fmt.Errorf("failed to create %s %s/%s: %w",
					obj.GetKind(), obj.GetNamespace(), obj.GetName(), err)
			}
		} else {
			// Resource exists, patch via SSA
			opts := []client.ApplyOption{
				client.FieldOwner("diverge-direct-deployer"),
				client.ForceOwnership,
			}
			if err := d.Client.Apply(ctx, client.ApplyConfigurationFromUnstructured(obj), opts...); err != nil {
				return fmt.Errorf("failed to apply %s %s/%s: %w",
					obj.GetKind(), obj.GetNamespace(), obj.GetName(), err)
			}
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
// CR3: Checks ObservedGeneration and terminal failure conditions.
func deploymentHealth(dep *appsv1.Deployment) string {
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}

	// Check for terminal rollout failure conditions first
	for _, cond := range dep.Status.Conditions {
		if cond.Type == appsv1.DeploymentReplicaFailure && cond.Status == "True" {
			return "Degraded"
		}
		if cond.Type == appsv1.DeploymentProgressing && cond.Status == "False" {
			return "Degraded"
		}
	}

	// Don't report healthy if the controller hasn't observed the latest generation
	if dep.Status.ObservedGeneration < dep.Generation {
		return "Progressing"
	}

	if dep.Status.AvailableReplicas >= desired && dep.Status.UpdatedReplicas >= desired {
		return "Healthy"
	}
	return "Progressing"
}

// statefulSetHealth determines the health of a StatefulSet.
// CR3: Checks ObservedGeneration before declaring healthy.
func statefulSetHealth(sts *appsv1.StatefulSet) string {
	desired := int32(1)
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}

	// Don't report healthy if the controller hasn't observed the latest generation
	if sts.Status.ObservedGeneration < sts.Generation {
		return "Progressing"
	}

	if sts.Status.ReadyReplicas >= desired && sts.Status.UpdatedReplicas >= desired {
		return "Healthy"
	}
	if sts.Status.ReadyReplicas == 0 && sts.Generation > 1 {
		return "Degraded"
	}
	return "Progressing"
}
