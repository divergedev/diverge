package deployer

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/multicluster"
)

// defaultAllowedKinds is an allowlist of Kubernetes resource kinds permitted in
// preview deployment manifests. Only namespaced workload resources that
// Diverge needs to deploy are allowed. Any kind not in this list —
// including cluster-scoped resources, RBAC (Role/RoleBinding), endpoint
// hijacking resources (Endpoints/EndpointSlice), and custom CRDs — is rejected.
var defaultAllowedKinds = map[string]bool{
	"Deployment":              true,
	"StatefulSet":             true,
	"DaemonSet":               true,
	"ReplicaSet":              true,
	"Job":                     true,
	"CronJob":                 true,
	"Service":                 true,
	"ConfigMap":               true,
	"Secret":                  true,
	"Ingress":                 true,
	"ServiceAccount":          true,
	"NetworkPolicy":           true,
	"PersistentVolumeClaim":   true,
	"HorizontalPodAutoscaler": true,
	"PodDisruptionBudget":     true,
}

// DirectDeployerOption configures a DirectDeployer.
type DirectDeployerOption func(*DirectDeployer)

// WithAllowedKinds overrides the default allowlist of Kubernetes resource kinds.
func WithAllowedKinds(kinds map[string]bool) DirectDeployerOption {
	return func(d *DirectDeployer) {
		cloned := make(map[string]bool, len(kinds))
		for k, v := range kinds {
			cloned[k] = v
		}
		d.AllowedKinds = cloned
		d.customAllowedKinds = true
	}
}

// WithClusterClients configures multi-cluster spoke client resolution on a DirectDeployer.
func WithClusterClients(resolver multicluster.Resolver) DirectDeployerOption {
	return func(d *DirectDeployer) {
		d.ClusterClients = resolver
	}
}

// DirectDeployer applies pre-rendered Kubernetes manifests directly
// via Server-Side Apply, without requiring ArgoCD.
type DirectDeployer struct {
	Client             client.Client
	Fetcher            ManifestFetcher
	ClusterClients     multicluster.Resolver
	AllowedKinds       map[string]bool
	customAllowedKinds bool
}

// NewDirectDeployer creates a new DirectDeployer with optional configuration.
func NewDirectDeployer(c client.Client, fetcher ManifestFetcher, opts ...DirectDeployerOption) *DirectDeployer {
	d := &DirectDeployer{
		Client:  c,
		Fetcher: fetcher,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(d)
		}
	}
	return d
}

// WithAllowedKinds sets custom allowed kinds on the DirectDeployer and returns it for chaining.
func (d *DirectDeployer) WithAllowedKinds(kinds map[string]bool) *DirectDeployer {
	WithAllowedKinds(kinds)(d)
	return d
}

func (d *DirectDeployer) isKindPermitted(kind string) bool {
	if d.customAllowedKinds || d.AllowedKinds != nil {
		return d.AllowedKinds[kind]
	}
	return defaultAllowedKinds[kind]
}

func (d *DirectDeployer) resolveClient(ctx context.Context, env *v1alpha1.Environment) (client.Client, bool, error) {
	if pinned, ok := multicluster.TargetFromContext(ctx); ok {
		return pinned.Client, pinned.IsRemote, nil
	}
	if !multicluster.IsRemoteEnv(env) {
		return d.Client, false, nil
	}
	resolver := d.ClusterClients
	if resolver == nil && d.Client != nil {
		resolver = multicluster.NewManager(d.Client, nil, nil, d.Client.Scheme())
	}
	if resolver == nil {
		return nil, false, fmt.Errorf("multi-cluster resolver is not configured")
	}
	target, err := resolver.Resolve(ctx, env)
	if err != nil {
		return nil, false, err
	}
	return target.Client, target.IsRemote, nil
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

	// Determine target namespace and cluster client
	targetClient, isRemote, err := d.resolveClient(ctx, env)
	if err != nil {
		return fmt.Errorf("failed to resolve target cluster: %w", err)
	}

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	logger.Info("Deploying manifests", "count", len(objects), "namespace", targetNS, "remoteCluster", isRemote)

	for i := range objects {
		obj := &objects[i]

		// S3: Only permit allowlisted resource kinds in preview deployments.
		// This prevents privilege escalation via cluster-scoped resources
		// (ClusterRole, Namespace, CRD), RBAC (Role, RoleBinding), and endpoint hijacking.
		kind := obj.GetObjectKind().GroupVersionKind().Kind
		if !d.isKindPermitted(kind) {
			return fmt.Errorf("resource kind %q is not permitted in preview deployments (only workload resources are allowed)", kind)
		}

		// CR2: Enforce namespace scope — override empty namespaces and
		// reject mismatched namespaces to prevent cross-namespace writes.
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
		existingLabels["divergedev.com/environment"] = env.Name
		existingLabels["divergedev.com/managed-by"] = "diverge"
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

		// Inject baseline fingerprint into workload pod template annotations so drift-policy=sync triggers rolling restart
		if fp := strings.TrimSpace(env.Annotations["divergedev.com/baseline-fingerprint"]); fp != "" {
			kind := obj.GetKind()
			// Job pod templates are immutable; changing them breaks the apply.
			if kind == "Deployment" || kind == "StatefulSet" || kind == "DaemonSet" {
				annos, found, _ := unstructured.NestedStringMap(obj.Object, "spec", "template", "metadata", "annotations")
				if !found || annos == nil {
					annos = make(map[string]string)
				}
				annos["divergedev.com/baseline-fingerprint"] = fp
				_ = unstructured.SetNestedStringMap(obj.Object, annos, "spec", "template", "metadata", "annotations")
			}
		}

		if strings.EqualFold(strings.TrimSpace(env.Annotations["divergedev.com/hibernated"]), "true") {
			kind := obj.GetKind()
			if kind == "Deployment" || kind == "StatefulSet" {
				_ = unstructured.SetNestedField(obj.Object, int64(0), "spec", "replicas")
			}
		}

		// Set OwnerReference for local 'same' namespace mode only.
		// CR2: Never set OwnerReferences across namespaces or on remote spoke clusters,
		// where the hub Environment UID does not exist and K8s GC would delete the workload.
		if !isRemote && env.Spec.Deploy.Namespace != "create" && obj.GetNamespace() == env.Namespace {
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
		err = targetClient.Get(ctx, client.ObjectKeyFromObject(obj), existing)
		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to check existing %s %s/%s: %w",
					obj.GetKind(), obj.GetNamespace(), obj.GetName(), err)
			}
			// Resource doesn't exist, create it
			if err := targetClient.Create(ctx, obj); err != nil {
				return fmt.Errorf("failed to create %s %s/%s: %w",
					obj.GetKind(), obj.GetNamespace(), obj.GetName(), err)
			}
		} else {
			// Resource exists, patch via SSA
			opts := []client.ApplyOption{
				client.FieldOwner("diverge-direct-deployer"),
				client.ForceOwnership,
			}
			if err := targetClient.Apply(ctx, client.ApplyConfigurationFromUnstructured(obj), opts...); err != nil {
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

// Teardown deletes managed workload and Service resources for the environment so
// EnvironmentReconciler.handleTeardown's Status gate drains cleanly before
// finalizer removal and preview namespace deletion.
func (d *DirectDeployer) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	logger := log.FromContext(ctx).WithName("direct-deployer")
	logger.Info("Teardown requested",
		"namespace_mode", env.Spec.Deploy.Namespace,
		"environment", env.Name,
	)
	if d.Client == nil && d.ClusterClients == nil {
		return nil
	}

	targetClient, _, err := d.resolveClient(ctx, env)
	if err != nil {
		if _, isVal := multicluster.IsValidationError(err); isVal && !multicluster.HasConfirmedRemoteTarget(env) {
			return nil
		}
		return fmt.Errorf("failed to resolve target cluster for teardown: %w", err)
	}
	if targetClient == nil {
		return nil
	}

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	selector := labels.SelectorFromSet(map[string]string{
		"divergedev.com/environment": env.Name,
		"divergedev.com/managed-by":  "diverge",
	})
	listOpts := []client.ListOption{
		client.InNamespace(targetNS),
		client.MatchingLabelsSelector{Selector: selector},
	}
	fgDelOpts := []client.DeleteOption{
		client.PropagationPolicy(metav1.DeletePropagationForeground),
	}

	var deployList appsv1.DeploymentList
	if err := targetClient.List(ctx, &deployList, listOpts...); err != nil {
		return fmt.Errorf("failed to list deployments for teardown: %w", err)
	}
	for i := range deployList.Items {
		if err := targetClient.Delete(ctx, &deployList.Items[i], fgDelOpts...); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete deployment %s/%s: %w", targetNS, deployList.Items[i].Name, err)
		}
	}

	var stsList appsv1.StatefulSetList
	if err := targetClient.List(ctx, &stsList, listOpts...); err != nil {
		return fmt.Errorf("failed to list statefulsets for teardown: %w", err)
	}
	for i := range stsList.Items {
		if err := targetClient.Delete(ctx, &stsList.Items[i], fgDelOpts...); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete statefulset %s/%s: %w", targetNS, stsList.Items[i].Name, err)
		}
	}

	var dsList appsv1.DaemonSetList
	if err := targetClient.List(ctx, &dsList, listOpts...); isIgnorableListErr(err) != nil {
		return fmt.Errorf("failed to list daemonsets for teardown: %w", err)
	}
	for i := range dsList.Items {
		if err := targetClient.Delete(ctx, &dsList.Items[i], fgDelOpts...); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete daemonset %s/%s: %w", targetNS, dsList.Items[i].Name, err)
		}
	}

	var rsList appsv1.ReplicaSetList
	if err := targetClient.List(ctx, &rsList, listOpts...); isIgnorableListErr(err) != nil {
		return fmt.Errorf("failed to list replicasets for teardown: %w", err)
	}
	for i := range rsList.Items {
		if err := targetClient.Delete(ctx, &rsList.Items[i], fgDelOpts...); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete replicaset %s/%s: %w", targetNS, rsList.Items[i].Name, err)
		}
	}

	var cronJobList batchv1.CronJobList
	if err := targetClient.List(ctx, &cronJobList, listOpts...); isIgnorableListErr(err) != nil {
		return fmt.Errorf("failed to list cronjobs for teardown: %w", err)
	}
	for i := range cronJobList.Items {
		if err := targetClient.Delete(ctx, &cronJobList.Items[i], fgDelOpts...); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete cronjob %s/%s: %w", targetNS, cronJobList.Items[i].Name, err)
		}
	}

	var jobList batchv1.JobList
	if err := targetClient.List(ctx, &jobList, listOpts...); err != nil {
		return fmt.Errorf("failed to list jobs for teardown: %w", err)
	}
	for i := range jobList.Items {
		if err := targetClient.Delete(ctx, &jobList.Items[i], fgDelOpts...); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete job %s/%s: %w", targetNS, jobList.Items[i].Name, err)
		}
	}

	var hpaList autoscalingv2.HorizontalPodAutoscalerList
	if err := targetClient.List(ctx, &hpaList, listOpts...); isIgnorableListErr(err) != nil {
		return fmt.Errorf("failed to list horizontalpodautoscalers for teardown: %w", err)
	}
	for i := range hpaList.Items {
		if err := targetClient.Delete(ctx, &hpaList.Items[i]); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete horizontalpodautoscaler %s/%s: %w", targetNS, hpaList.Items[i].Name, err)
		}
	}

	var pdbList policyv1.PodDisruptionBudgetList
	if err := targetClient.List(ctx, &pdbList, listOpts...); isIgnorableListErr(err) != nil {
		return fmt.Errorf("failed to list poddisruptionbudgets for teardown: %w", err)
	}
	for i := range pdbList.Items {
		if err := targetClient.Delete(ctx, &pdbList.Items[i]); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete poddisruptionbudget %s/%s: %w", targetNS, pdbList.Items[i].Name, err)
		}
	}

	var ingressList networkingv1.IngressList
	if err := targetClient.List(ctx, &ingressList, listOpts...); isIgnorableListErr(err) != nil {
		return fmt.Errorf("failed to list ingresses for teardown: %w", err)
	}
	for i := range ingressList.Items {
		if err := targetClient.Delete(ctx, &ingressList.Items[i]); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete ingress %s/%s: %w", targetNS, ingressList.Items[i].Name, err)
		}
	}

	var netpolList networkingv1.NetworkPolicyList
	if err := targetClient.List(ctx, &netpolList, listOpts...); isIgnorableListErr(err) != nil {
		return fmt.Errorf("failed to list networkpolicies for teardown: %w", err)
	}
	for i := range netpolList.Items {
		// In create-namespace mode, diverge-default-netpol is removed with the preview namespace after workload drain.
		if env.Spec.Deploy.Namespace == "create" && netpolList.Items[i].Name == "diverge-default-netpol" {
			continue
		}
		if err := targetClient.Delete(ctx, &netpolList.Items[i]); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete networkpolicy %s/%s: %w", targetNS, netpolList.Items[i].Name, err)
		}
	}

	var svcList corev1.ServiceList
	if err := targetClient.List(ctx, &svcList, listOpts...); err != nil {
		return fmt.Errorf("failed to list services for teardown: %w", err)
	}
	for i := range svcList.Items {
		if err := targetClient.Delete(ctx, &svcList.Items[i]); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete service %s/%s: %w", targetNS, svcList.Items[i].Name, err)
		}
	}

	var cmList corev1.ConfigMapList
	if err := targetClient.List(ctx, &cmList, listOpts...); isIgnorableListErr(err) != nil {
		return fmt.Errorf("failed to list configmaps for teardown: %w", err)
	}
	for i := range cmList.Items {
		if isControllerDeferredConfigMap(env, cmList.Items[i].Name) {
			continue
		}
		if err := targetClient.Delete(ctx, &cmList.Items[i]); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete configmap %s/%s: %w", targetNS, cmList.Items[i].Name, err)
		}
	}

	var secretList corev1.SecretList
	if err := targetClient.List(ctx, &secretList, listOpts...); isIgnorableListErr(err) != nil {
		return fmt.Errorf("failed to list secrets for teardown: %w", err)
	}
	for i := range secretList.Items {
		if isControllerDeferredSecret(env, secretList.Items[i].Name) {
			continue
		}
		if err := targetClient.Delete(ctx, &secretList.Items[i]); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete secret %s/%s: %w", targetNS, secretList.Items[i].Name, err)
		}
	}

	var pvcList corev1.PersistentVolumeClaimList
	if err := targetClient.List(ctx, &pvcList, listOpts...); isIgnorableListErr(err) != nil {
		return fmt.Errorf("failed to list persistentvolumeclaims for teardown: %w", err)
	}
	for i := range pvcList.Items {
		if err := targetClient.Delete(ctx, &pvcList.Items[i]); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete persistentvolumeclaim %s/%s: %w", targetNS, pvcList.Items[i].Name, err)
		}
	}

	var saList corev1.ServiceAccountList
	if err := targetClient.List(ctx, &saList, listOpts...); isIgnorableListErr(err) != nil {
		return fmt.Errorf("failed to list serviceaccounts for teardown: %w", err)
	}
	for i := range saList.Items {
		if err := targetClient.Delete(ctx, &saList.Items[i]); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete serviceaccount %s/%s: %w", targetNS, saList.Items[i].Name, err)
		}
	}

	return nil
}

func isIgnorableListErr(err error) error {
	if err == nil || meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
		return nil
	}
	return err
}

func isControllerDeferredConfigMap(env *v1alpha1.Environment, name string) bool {
	if env == nil || env.DeletionTimestamp.IsZero() {
		return false
	}
	featuresCM := "diverge-features-" + env.Name
	if len(featuresCM) > 253 {
		featuresCM = featuresCM[:253]
	}
	bannerCM := "diverge-banner-" + env.Name
	if len(bannerCM) > 253 {
		bannerCM = bannerCM[:253]
	}
	return name == featuresCM || name == bannerCM
}

func isControllerDeferredSecret(env *v1alpha1.Environment, name string) bool {
	if env == nil || env.DeletionTimestamp.IsZero() {
		return false
	}
	return strings.HasPrefix(name, "diverge-db-env-") || strings.HasPrefix(name, "diverge-db-")
}

// Status returns the deployment status of resources managed by this deployer.
func (d *DirectDeployer) Status(ctx context.Context, env *v1alpha1.Environment) ([]ServiceStatus, error) {
	targetClient, _, err := d.resolveClient(ctx, env)
	if err != nil {
		if !env.DeletionTimestamp.IsZero() {
			if _, isVal := multicluster.IsValidationError(err); isVal && !multicluster.HasConfirmedRemoteTarget(env) {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("failed to resolve target cluster for status: %w", err)
	}

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	selector := labels.SelectorFromSet(map[string]string{
		"divergedev.com/environment": env.Name,
		"divergedev.com/managed-by":  "diverge",
	})

	var result []ServiceStatus

	// Check Deployments
	var deployList appsv1.DeploymentList
	if err := targetClient.List(ctx, &deployList,
		client.InNamespace(targetNS),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		return nil, fmt.Errorf("failed to list deployments: %w", err)
	}
	for _, dep := range deployList.Items {
		result = append(result, ServiceStatus{
			Name:       dep.Name,
			Service:    dep.Labels["divergedev.com/service"],
			SyncStatus: "Applied",
			Health:     deploymentHealth(&dep),
		})
	}

	// Check StatefulSets
	var stsList appsv1.StatefulSetList
	if err := targetClient.List(ctx, &stsList,
		client.InNamespace(targetNS),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		return nil, fmt.Errorf("failed to list statefulsets: %w", err)
	}
	for _, sts := range stsList.Items {
		result = append(result, ServiceStatus{
			Name:       sts.Name,
			Service:    sts.Labels["divergedev.com/service"],
			SyncStatus: "Applied",
			Health:     statefulSetHealth(&sts),
		})
	}

	// During teardown, keep the drain gate closed while managed Jobs or preview Pods
	// are still terminating so database Secrets and feature ConfigMaps are not deleted
	// out from under active containers.
	if !env.DeletionTimestamp.IsZero() {
		var jobList batchv1.JobList
		if err := targetClient.List(ctx, &jobList,
			client.InNamespace(targetNS),
			client.MatchingLabelsSelector{Selector: selector},
		); err != nil {
			return nil, fmt.Errorf("failed to list jobs: %w", err)
		}
		for _, job := range jobList.Items {
			result = append(result, ServiceStatus{
				Name:       job.Name,
				Service:    job.Labels["divergedev.com/service"],
				SyncStatus: "Terminating",
				Health:     "Progressing",
			})
		}

		podSelector := labels.SelectorFromSet(map[string]string{
			"divergedev.com/environment": env.Name,
		})
		var podList corev1.PodList
		if err := targetClient.List(ctx, &podList,
			client.InNamespace(targetNS),
			client.MatchingLabelsSelector{Selector: podSelector},
		); err != nil {
			return nil, fmt.Errorf("failed to list pods: %w", err)
		}
		for _, pod := range podList.Items {
			if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				continue
			}
			result = append(result, ServiceStatus{
				Name:       pod.Name,
				Service:    pod.Labels["divergedev.com/service"],
				SyncStatus: "Terminating",
				Health:     "Progressing",
			})
		}
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
