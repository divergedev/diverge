package deployer

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

type KNativeDeployer struct {
	Client client.Client
}

func (d *KNativeDeployer) targetNamespace(env *v1alpha1.Environment) string {
	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}
	return targetNS
}

func (d *KNativeDeployer) Deploy(ctx context.Context, env *v1alpha1.Environment) error {
	targetNS := d.targetNamespace(env)

	ksvc := &unstructured.Unstructured{}
	ksvc.SetGroupVersionKind(schema.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "Service"})
	ksvc.SetName(env.Name)
	ksvc.SetNamespace(targetNS)

	if env.Spec.Deploy.Namespace != "create" && targetNS == env.Namespace {
		ownerRef := metav1.OwnerReference{
			APIVersion: env.APIVersion,
			Kind:       env.Kind,
			Name:       env.Name,
			UID:        env.UID,
		}
		ksvc.SetOwnerReferences([]metav1.OwnerReference{ownerRef})
	}

	labels := ksvc.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["diverge.io/managed-by"] = "diverge"
	labels["diverge.io/environment"] = env.Name
	labels["networking.knative.dev/visibility"] = "cluster-local"
	ksvc.SetLabels(labels)

	image := ""
	if env.Spec.ServiceConfig != nil {
		image = env.Spec.ServiceConfig.Image
	}

	err := unstructured.SetNestedStringMap(ksvc.Object, map[string]string{
		"autoscaling.knative.dev/minScale": "0",
	}, "spec", "template", "metadata", "annotations")
	if err != nil {
		return fmt.Errorf("failed to set minScale annotation: %w", err)
	}

	err = unstructured.SetNestedSlice(ksvc.Object, []interface{}{
		map[string]interface{}{
			"image": image,
		},
	}, "spec", "template", "spec", "containers")
	if err != nil {
		return fmt.Errorf("failed to set container image: %w", err)
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(schema.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "Service"})
	err = d.Client.Get(ctx, client.ObjectKey{Name: env.Name, Namespace: targetNS}, existing)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to check existing knative service: %w", err)
		}
		if err := d.Client.Create(ctx, ksvc); err != nil {
			return fmt.Errorf("failed to create knative service: %w", err)
		}
	} else {
		err = d.Client.Apply(ctx, client.ApplyConfigurationFromUnstructured(ksvc), client.FieldOwner("diverge"), client.ForceOwnership)
		if err != nil {
			return fmt.Errorf("failed to apply knative service: %w", err)
		}
	}

	return nil
}

func (d *KNativeDeployer) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	targetNS := d.targetNamespace(env)

	ksvc := &unstructured.Unstructured{}
	ksvc.SetGroupVersionKind(schema.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "Service"})
	ksvc.SetName(env.Name)
	ksvc.SetNamespace(targetNS)

	err := d.Client.Delete(ctx, ksvc)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete knative service: %w", err)
	}

	return nil
}

func (d *KNativeDeployer) Status(ctx context.Context, env *v1alpha1.Environment) ([]ServiceStatus, error) {
	targetNS := d.targetNamespace(env)

	ksvc := &unstructured.Unstructured{}
	ksvc.SetGroupVersionKind(schema.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "Service"})

	err := d.Client.Get(ctx, client.ObjectKey{Name: env.Name, Namespace: targetNS}, ksvc)

	status := ServiceStatus{
		Name:       env.Name,
		Service:    env.Name,
		SyncStatus: "Applied",
	}

	if err != nil {
		if apierrors.IsNotFound(err) {
			status.Health = "Missing"
			return []ServiceStatus{status}, nil
		}
		return nil, fmt.Errorf("failed to get knative service: %w", err)
	}

	health := "Progressing"
	conditions, found, err := unstructured.NestedSlice(ksvc.Object, "status", "conditions")
	if err == nil && found {
		for _, c := range conditions {
			cond, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			t, _ := cond["type"].(string)
			s, _ := cond["status"].(string)
			if t == "Ready" {
				switch s {
				case "True":
					health = "Healthy"
				case "False":
					health = "Degraded"
				case "Unknown":
					health = "Progressing"
				}
				break
			}
		}
	}
	status.Health = health

	url, found, err := unstructured.NestedString(ksvc.Object, "status", "url")
	if err == nil && found && url != "" {
		status.URL = url
		status.Message = fmt.Sprintf("status.url: %s", url)
	}

	return []ServiceStatus{status}, nil
}
