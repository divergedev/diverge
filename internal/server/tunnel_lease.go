package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

// TunnelLease provides distributed fencing for tunnel ownership using K8s Leases.
// When multiple server replicas exist, only one can own a tunnel at a time.
type TunnelLease struct {
	k8sClient kubernetes.Interface
	logger    *slog.Logger
	podName   string
}

func NewTunnelLease(k8s kubernetes.Interface, logger *slog.Logger, podName string) *TunnelLease {
	return &TunnelLease{
		k8sClient: k8s,
		logger:    logger,
		podName:   podName,
	}
}

func tunnelLeaseName(previewID string) string {
	return fmt.Sprintf("diverge-tunnel-lease-%s", previewID)
}

// Acquire attempts to acquire or steal the lease for a tunnel.
// Returns the previous holder (empty if none) and any error.
func (tl *TunnelLease) Acquire(ctx context.Context, namespace, previewID, tunnelID string) (previousHolder string, err error) {
	leaseName := tunnelLeaseName(previewID)
	holderIdentity := fmt.Sprintf("%s:%s", tl.podName, tunnelID)
	now := metav1.NewMicroTime(time.Now())
	leaseDuration := int32(60) // seconds

	existing, err := tl.k8sClient.CoordinationV1().Leases(namespace).Get(ctx, leaseName, metav1.GetOptions{})
	if err == nil {
		// Lease exists — steal it
		previousHolder = ""
		if existing.Spec.HolderIdentity != nil {
			previousHolder = *existing.Spec.HolderIdentity
		}

		existing.Spec.HolderIdentity = &holderIdentity
		existing.Spec.AcquireTime = &now
		existing.Spec.RenewTime = &now
		existing.Spec.LeaseDurationSeconds = &leaseDuration

		_, err = tl.k8sClient.CoordinationV1().Leases(namespace).Update(ctx, existing, metav1.UpdateOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to update tunnel lease: %w", err)
		}
		tl.logger.Info("tunnel lease acquired (stolen)", "preview-id", previewID, "previous", previousHolder)
		return previousHolder, nil
	}

	// Lease doesn't exist — create it
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: namespace,
			Labels: map[string]string{
				"diverge.dev/tunnel":     "true",
				"diverge.dev/preview-id": previewID,
			},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &holderIdentity,
			LeaseDurationSeconds: &leaseDuration,
			AcquireTime:          &now,
			RenewTime:            &now,
			LeaseTransitions:     ptr.To(int32(0)),
		},
	}

	_, err = tl.k8sClient.CoordinationV1().Leases(namespace).Create(ctx, lease, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create tunnel lease: %w", err)
	}
	tl.logger.Info("tunnel lease acquired (new)", "preview-id", previewID)
	return "", nil
}

// Renew extends the lease. Returns false if we no longer hold it.
func (tl *TunnelLease) Renew(ctx context.Context, namespace, previewID, tunnelID string) bool {
	leaseName := tunnelLeaseName(previewID)
	holderIdentity := fmt.Sprintf("%s:%s", tl.podName, tunnelID)

	existing, err := tl.k8sClient.CoordinationV1().Leases(namespace).Get(ctx, leaseName, metav1.GetOptions{})
	if err != nil {
		tl.logger.Warn("failed to get lease for renewal", "err", err)
		return false
	}

	// Check if we still hold it
	if existing.Spec.HolderIdentity == nil || *existing.Spec.HolderIdentity != holderIdentity {
		tl.logger.Info("lease lost to another holder", "preview-id", previewID)
		return false
	}

	now := metav1.NewMicroTime(time.Now())
	existing.Spec.RenewTime = &now
	_, err = tl.k8sClient.CoordinationV1().Leases(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		tl.logger.Warn("failed to renew lease", "err", err)
		return false
	}
	return true
}

// Release gives up the lease.
func (tl *TunnelLease) Release(ctx context.Context, namespace, previewID, tunnelID string) {
	leaseName := tunnelLeaseName(previewID)
	holderIdentity := fmt.Sprintf("%s:%s", tl.podName, tunnelID)

	existing, err := tl.k8sClient.CoordinationV1().Leases(namespace).Get(ctx, leaseName, metav1.GetOptions{})
	if err != nil {
		return
	}

	// Only delete if we still hold it
	if existing.Spec.HolderIdentity != nil && *existing.Spec.HolderIdentity == holderIdentity {
		_ = tl.k8sClient.CoordinationV1().Leases(namespace).Delete(ctx, leaseName, metav1.DeleteOptions{})
	}
}
