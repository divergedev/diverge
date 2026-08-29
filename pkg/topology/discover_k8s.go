package topology

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func DiscoverPrometheusURL(ctx context.Context, client kubernetes.Interface) (string, error) {
	// PRIMARY: label selector
	services, err := client.CoreV1().Services("").List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=prometheus",
	})
	if err == nil && len(services.Items) > 0 {
		svc := services.Items[0]
		port := int32(80)
		if len(svc.Spec.Ports) > 0 {
			port = svc.Spec.Ports[0].Port
		}
		return fmt.Sprintf("http://%s.%s.svc:%d", svc.Name, svc.Namespace, port), nil
	}

	// FALLBACK: name pattern matching
	namespaces := []string{"monitoring", "istio-system", "linkerd", "cilium", "default"}
	for _, ns := range namespaces {
		svcs, err := client.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		for _, svc := range svcs.Items {
			if strings.Contains(svc.Name, "prometheus") {
				port := int32(80)
				if len(svc.Spec.Ports) > 0 {
					port = svc.Spec.Ports[0].Port
				}
				return fmt.Sprintf("http://%s.%s.svc:%d", svc.Name, svc.Namespace, port), nil
			}
		}
	}

	return "", fmt.Errorf("prometheus service not found")
}
