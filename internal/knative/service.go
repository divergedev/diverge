package knative

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
)

// BuildKnativeService builds a Knative Service for a preview environment pod.
func BuildKnativeService(name, namespace, image string, port int32, labels, annotations map[string]string) (*knservingv1.Service, error) {
	if labels == nil {
		labels = make(map[string]string)
	} else {
		for k, v := range labels {
			if errs := validation.IsQualifiedName(k); len(errs) > 0 {
				return nil, fmt.Errorf("invalid label key %q: %v", k, errs)
			}
			if errs := validation.IsValidLabelValue(v); len(errs) > 0 {
				return nil, fmt.Errorf("invalid label value %q: %v", v, errs)
			}
		}
	}
	// Add diverge.io/managed-by labels
	labels["diverge.io/managed-by"] = "diverge"

	if annotations == nil {
		annotations = make(map[string]string)
	}
	// Adds ArgoCD IgnoreExtraneous annotation
	annotations["argocd.argoproj.io/compare-options"] = "IgnoreExtraneous"

	// Sets minScale based on config
	annotations["autoscaling.knative.dev/min-scale"] = "0"

	return &knservingv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: knservingv1.ServiceSpec{
			ConfigurationSpec: knservingv1.ConfigurationSpec{
				Template: knservingv1.RevisionTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: annotations,
						Labels:      labels,
					},
					Spec: knservingv1.RevisionSpec{
						PodSpec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: image,
									Ports: []corev1.ContainerPort{
										{
											ContainerPort: port,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}, nil
}
