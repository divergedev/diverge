package server

import (
	"context"
	"log/slog"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// TunnelGC periodically garbage-collects expired tunnel K8s resources.
type TunnelGC struct {
	k8sClient      kubernetes.Interface
	logger         *slog.Logger
	interval       time.Duration
	createServices bool
}

func NewTunnelGC(k8s kubernetes.Interface, logger *slog.Logger) *TunnelGC {
	if logger == nil {
		logger = slog.Default()
	}
	return &TunnelGC{
		k8sClient:      k8s,
		logger:         logger,
		interval:       30 * time.Second,
		createServices: true,
	}
}

// WithCreateServices records whether per-tunnel K8s Services are actively created.
// Legacy Services, EndpointSlices, and Endpoints carrying the tunnel-expires annotation
// are always swept so upgrades from Service-creating mode do not orphan old resources.
func (gc *TunnelGC) WithCreateServices(enabled bool) *TunnelGC {
	gc.createServices = enabled
	return gc
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

func deletePreconditions(uid types.UID, rv string) metav1.DeleteOptions {
	pre := &metav1.Preconditions{
		ResourceVersion: &rv,
	}
	if uid != "" {
		u := uid
		pre.UID = &u
	}
	return metav1.DeleteOptions{Preconditions: pre}
}

func (gc *TunnelGC) sweep(ctx context.Context, namespace string) {
	if gc.k8sClient == nil {
		return
	}
	now := time.Now()

	// Always sweep Services carrying tunnel-expires so legacy/orphaned Services from
	// createServices=true upgrades are cleaned up too (never abort Lease sweeps on list error).
	svcs, err := gc.k8sClient.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "divergedev.com/tunnel=true",
	})
	if err != nil {
		gc.logger.Warn("tunnel GC: failed to list services", "ns", namespace, "err", err)
	} else {
		for _, svc := range svcs.Items {
			expiresStr, ok := svc.Annotations["divergedev.com/tunnel-expires"]
			if !ok {
				continue
			}
			expires, parseErr := time.Parse(time.RFC3339, expiresStr)
			if parseErr != nil {
				gc.logger.Warn("tunnel GC: bad expires annotation", "svc", svc.Name, "err", parseErr)
				continue
			}
			if now.After(expires) {
				gc.logger.Info("tunnel GC: deleting expired service", "svc", svc.Name, "ns", svc.Namespace)
				if delErr := gc.k8sClient.CoreV1().Services(svc.Namespace).Delete(ctx, svc.Name, deletePreconditions(svc.UID, svc.ResourceVersion)); delErr != nil && !apierrors.IsNotFound(delErr) && !apierrors.IsConflict(delErr) {
					gc.logger.Warn("tunnel GC: failed to delete service", "svc", svc.Name, "err", delErr)
				}
			}
		}
	}

	// Sweep EndpointSlices
	eps, err := gc.k8sClient.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "divergedev.com/tunnel=true",
	})
	if err != nil {
		gc.logger.Warn("tunnel GC: failed to list endpointslices", "ns", namespace, "err", err)
	} else {
		for _, ep := range eps.Items {
			expiresStr, ok := ep.Annotations["divergedev.com/tunnel-expires"]
			if !ok {
				continue
			}
			expires, parseErr := time.Parse(time.RFC3339, expiresStr)
			if parseErr != nil {
				continue
			}
			if now.After(expires) {
				gc.logger.Info("tunnel GC: deleting expired endpointslice", "ep", ep.Name, "ns", ep.Namespace)
				if delErr := gc.k8sClient.DiscoveryV1().EndpointSlices(ep.Namespace).Delete(ctx, ep.Name, deletePreconditions(ep.UID, ep.ResourceVersion)); delErr != nil && !apierrors.IsNotFound(delErr) && !apierrors.IsConflict(delErr) {
					gc.logger.Warn("tunnel GC: failed to delete endpointslice", "ep", ep.Name, "err", delErr)
				}
			}
		}
	}

	// Sweep Endpoints (legacy core/v1 for kube-dns clusters)
	endpointsList, err := gc.k8sClient.CoreV1().Endpoints(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "divergedev.com/tunnel=true",
	})
	if err != nil {
		gc.logger.Warn("tunnel GC: failed to list endpoints", "ns", namespace, "err", err)
	} else {
		for _, ep := range endpointsList.Items {
			expiresStr, ok := ep.Annotations["divergedev.com/tunnel-expires"]
			if !ok {
				continue
			}
			expires, parseErr := time.Parse(time.RFC3339, expiresStr)
			if parseErr != nil {
				continue
			}
			if now.After(expires) {
				gc.logger.Info("tunnel GC: deleting expired endpoints", "endpoints", ep.Name, "ns", ep.Namespace)
				if delErr := gc.k8sClient.CoreV1().Endpoints(ep.Namespace).Delete(ctx, ep.Name, deletePreconditions(ep.UID, ep.ResourceVersion)); delErr != nil && !apierrors.IsNotFound(delErr) && !apierrors.IsConflict(delErr) {
					gc.logger.Warn("tunnel GC: failed to delete endpoints", "endpoints", ep.Name, "err", delErr)
				}
			}
		}
	}

	// Sweep Leases
	leases, err := gc.k8sClient.CoordinationV1().Leases(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "divergedev.com/tunnel=true",
	})
	if err != nil {
		gc.logger.Warn("tunnel GC: failed to list leases", "ns", namespace, "err", err)
		return
	}
	for _, lease := range leases.Items {
		expired := false
		if expiresStr, ok := lease.Annotations["divergedev.com/tunnel-expires"]; ok {
			expires, parseErr := time.Parse(time.RFC3339, expiresStr)
			if parseErr != nil {
				continue
			}
			expired = now.After(expires)
		} else if lease.Spec.RenewTime != nil && lease.Spec.LeaseDurationSeconds != nil {
			expires := lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
			expired = now.After(expires)
		}
		if expired {
			gc.logger.Info("tunnel GC: deleting expired lease", "lease", lease.Name, "ns", lease.Namespace)
			if delErr := gc.k8sClient.CoordinationV1().Leases(lease.Namespace).Delete(ctx, lease.Name, deletePreconditions(lease.UID, lease.ResourceVersion)); delErr != nil && !apierrors.IsNotFound(delErr) && !apierrors.IsConflict(delErr) {
				gc.logger.Warn("tunnel GC: failed to delete lease", "lease", lease.Name, "err", delErr)
			}
		}
	}
}
