package server

import (
	"context"
	"log/slog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// TunnelGC periodically garbage-collects expired tunnel K8s resources.
// Resources are annotated with diverge.dev/tunnel-expires (RFC3339).
// If the annotation is past, the resource is deleted.
type TunnelGC struct {
	k8sClient kubernetes.Interface
	logger    *slog.Logger
	interval  time.Duration
}

func NewTunnelGC(k8s kubernetes.Interface, logger *slog.Logger) *TunnelGC {
	return &TunnelGC{
		k8sClient: k8s,
		logger:    logger,
		interval:  30 * time.Second,
	}
}

// Run starts the GC loop. Blocks until ctx is cancelled.
func (gc *TunnelGC) Run(ctx context.Context, namespaces []string) {
	ticker := time.NewTicker(gc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, ns := range namespaces {
				gc.sweep(ctx, ns)
			}
		}
	}
}

func (gc *TunnelGC) sweep(ctx context.Context, namespace string) {
	now := time.Now()

	// Sweep Services
	svcs, err := gc.k8sClient.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "diverge.dev/tunnel=true",
	})
	if err != nil {
		gc.logger.Warn("tunnel GC: failed to list services", "ns", namespace, "err", err)
		return
	}
	for _, svc := range svcs.Items {
		expiresStr, ok := svc.Annotations["diverge.dev/tunnel-expires"]
		if !ok {
			continue
		}
		expires, err := time.Parse(time.RFC3339, expiresStr)
		if err != nil {
			gc.logger.Warn("tunnel GC: bad expires annotation", "svc", svc.Name, "err", err)
			continue
		}
		if now.After(expires) {
			gc.logger.Info("tunnel GC: deleting expired service", "svc", svc.Name, "ns", namespace)
			if err := gc.k8sClient.CoreV1().Services(namespace).Delete(ctx, svc.Name, metav1.DeleteOptions{}); err != nil {
				gc.logger.Warn("tunnel GC: failed to delete service", "svc", svc.Name, "err", err)
			}
		}
	}

	// Sweep EndpointSlices
	eps, err := gc.k8sClient.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "diverge.dev/tunnel=true",
	})
	if err != nil {
		gc.logger.Warn("tunnel GC: failed to list endpointslices", "ns", namespace, "err", err)
		return
	}
	for _, ep := range eps.Items {
		expiresStr, ok := ep.Annotations["diverge.dev/tunnel-expires"]
		if !ok {
			continue
		}
		expires, err := time.Parse(time.RFC3339, expiresStr)
		if err != nil {
			continue
		}
		if now.After(expires) {
			gc.logger.Info("tunnel GC: deleting expired endpointslice", "ep", ep.Name, "ns", namespace)
			if err := gc.k8sClient.DiscoveryV1().EndpointSlices(namespace).Delete(ctx, ep.Name, metav1.DeleteOptions{}); err != nil {
				gc.logger.Warn("tunnel GC: failed to delete endpointslice", "ep", ep.Name, "err", err)
			}
		}
	}

	// Sweep Leases
	leases, err := gc.k8sClient.CoordinationV1().Leases(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "diverge.dev/tunnel=true",
	})
	if err != nil {
		return
	}
	for _, lease := range leases.Items {
		expiresStr, ok := lease.Annotations["diverge.dev/tunnel-expires"]
		if !ok {
			continue
		}
		expires, err := time.Parse(time.RFC3339, expiresStr)
		if err != nil {
			continue
		}
		if now.After(expires) {
			gc.logger.Info("tunnel GC: deleting expired lease", "lease", lease.Name, "ns", namespace)
			_ = gc.k8sClient.CoordinationV1().Leases(namespace).Delete(ctx, lease.Name, metav1.DeleteOptions{})
		}
	}
}
