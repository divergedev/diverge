//go:build e2e

package e2e

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

// echoDeployment returns a Deployment running hashicorp/http-echo that
// responds with the given message on the specified port.
func echoDeployment(name, namespace string, port int32) *appsv1.Deployment {
	labels := map[string]string{"app": name}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "echo",
						Image: "hashicorp/http-echo:0.2.3",
						Args:  []string{"-listen", formatPort(port), "-text", name},
						Ports: []corev1.ContainerPort{{
							ContainerPort: port,
						}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/",
									Port: intstr.FromInt32(port),
								},
							},
							InitialDelaySeconds: 1,
							PeriodSeconds:       2,
						},
					}},
				},
			},
		},
	}
}

// echoService returns a ClusterIP Service targeting the echo deployment.
func echoService(name, namespace string, port int32) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports: []corev1.ServicePort{{
				Port:       port,
				TargetPort: intstr.FromInt32(port),
			}},
		},
	}
}

// formatPort returns a port string like ":8080".
func formatPort(port int32) string {
	return fmt.Sprintf(":%d", port)
}
