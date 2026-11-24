package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const tunnelLeaseDurationSeconds int32 = 60

// ErrTunnelConflict is returned when an active unexpired tunnel Lease already exists
// for a different machine/owner and Force was not requested.
var ErrTunnelConflict = errors.New("tunnel lease already held by another active client")

// RenewResult distinguishes healthy renewal, external eviction, and transient K8s errors.
type RenewResult int

const (
	RenewOK RenewResult = iota
	RenewEvicted
	RenewTransientError
)

// LeaseAcquireOptions configures atomic Lease acquisition / fencing.
type LeaseAcquireOptions struct {
	Namespace           string
	PreviewID           string
	Service             string
	LeaseName           string
	TunnelID            string
	ClientInstanceID    string
	Owner               string
	K8sServiceName      string
	PodIP               string
	Port                int32
	Protocol            string
	MetadataJSON        string
	Force               bool
	CheckCrossUserForce func(existingOwner string) error
}

// LeaseAcquireResult describes the acquired or updated K8s Lease.
type LeaseAcquireResult struct {
	LeaseName        string
	LeaseUID         types.UID
	HolderIdentity   string
	LeaseTransitions int32
	PreviousHolder   string
	PreviousOwner    string
	Evicted          bool
}

// TunnelLease provides distributed fencing for tunnel ownership using K8s Leases.
// When multiple server replicas exist, only one can own a tunnel at a time.
type TunnelLease struct {
	k8sClient kubernetes.Interface
	logger    *slog.Logger
	podName   string
}

func NewTunnelLease(k8s kubernetes.Interface, logger *slog.Logger, podName string) *TunnelLease {
	if logger == nil {
		logger = slog.Default()
	}
	return &TunnelLease{
		k8sClient: k8s,
		logger:    logger,
		podName:   podName,
	}
}

func buildTunnelLeaseMeta(opts LeaseAcquireOptions, podName string, startedAt time.Time, generation int32) (map[string]string, map[string]string) {
	labels := map[string]string{
		"divergedev.com/tunnel":     "true",
		"divergedev.com/preview-id": opts.PreviewID,
	}
	if opts.Service != "" {
		labels["divergedev.com/service"] = opts.Service
	}

	annotations := map[string]string{
		"divergedev.com/tunnel-id":         opts.TunnelID,
		"divergedev.com/tunnel-holder-pod": podName,
		"divergedev.com/tunnel-port":       strconv.Itoa(int(opts.Port)),
		"divergedev.com/tunnel-started":    startedAt.UTC().Format(time.RFC3339),
		"divergedev.com/tunnel-generation": strconv.FormatInt(int64(generation), 10),
	}
	if opts.ClientInstanceID != "" {
		annotations["divergedev.com/tunnel-client-id"] = opts.ClientInstanceID
	}
	if opts.Owner != "" {
		annotations["divergedev.com/tunnel-owner"] = opts.Owner
	}
	if opts.K8sServiceName != "" {
		annotations["divergedev.com/tunnel-service-name"] = opts.K8sServiceName
	}
	if opts.PodIP != "" {
		annotations["divergedev.com/tunnel-holder-ip"] = opts.PodIP
	}
	if opts.Protocol != "" {
		annotations["divergedev.com/tunnel-protocol"] = opts.Protocol
	}
	if opts.MetadataJSON != "" {
		annotations["divergedev.com/tunnel-metadata"] = opts.MetadataJSON
	}
	return labels, annotations
}

// Acquire acquires a new Lease or fences/takes over an existing Lease for a tunnel.
func (tl *TunnelLease) Acquire(ctx context.Context, opts LeaseAcquireOptions) (*LeaseAcquireResult, error) {
	if tl.k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client is nil")
	}
	leaseName := opts.LeaseName
	if leaseName == "" {
		leaseName = tunnelLeaseName(opts.PreviewID, opts.Service)
	}

	holderIdentity := fmt.Sprintf("%s:%s", tl.podName, opts.TunnelID)
	leaseDuration := tunnelLeaseDurationSeconds

	const maxAcquireAttempts = 3
	for attempt := 0; attempt < maxAcquireAttempts; attempt++ {
		nowTime := time.Now()
		now := metav1.NewMicroTime(nowTime)

		existing, err := tl.k8sClient.CoordinationV1().Leases(opts.Namespace).Get(ctx, leaseName, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("failed to get tunnel lease: %w", err)
			}

			labels, annotations := buildTunnelLeaseMeta(opts, tl.podName, nowTime, 0)
			lease := &coordinationv1.Lease{
				ObjectMeta: metav1.ObjectMeta{
					Name:        leaseName,
					Namespace:   opts.Namespace,
					Labels:      labels,
					Annotations: annotations,
				},
				Spec: coordinationv1.LeaseSpec{
					HolderIdentity:       &holderIdentity,
					LeaseDurationSeconds: &leaseDuration,
					AcquireTime:          &now,
					RenewTime:            &now,
					LeaseTransitions:     ptr.To(int32(0)),
				},
			}

			created, createErr := tl.k8sClient.CoordinationV1().Leases(opts.Namespace).Create(ctx, lease, metav1.CreateOptions{})
			if createErr != nil {
				if (apierrors.IsAlreadyExists(createErr) || apierrors.IsConflict(createErr)) && attempt < maxAcquireAttempts-1 {
					continue
				}
				return nil, fmt.Errorf("failed to create tunnel lease: %w", createErr)
			}
			tl.logger.Info("tunnel lease acquired (new)", "lease", leaseName, "preview-id", opts.PreviewID, "service", opts.Service)
			return &LeaseAcquireResult{
				LeaseName:        leaseName,
				LeaseUID:         created.UID,
				HolderIdentity:   holderIdentity,
				LeaseTransitions: 0,
			}, nil
		}

		// Refuse takeover if the existing Lease declares labels for a different (preview, service) tuple.
		if existing.Labels != nil {
			existingPreview := existing.Labels["divergedev.com/preview-id"]
			existingService := existing.Labels["divergedev.com/service"]
			if (existingPreview != "" && (existingPreview != opts.PreviewID || existingService != opts.Service)) ||
				(existingService != "" && existingService != opts.Service) {
				return nil, fmt.Errorf("tunnel lease %q belongs to preview=%q service=%q, refusing takeover for preview=%q service=%q",
					leaseName, existingPreview, existingService, opts.PreviewID, opts.Service)
			}
		}

		previousHolder := ""
		if existing.Spec.HolderIdentity != nil {
			previousHolder = *existing.Spec.HolderIdentity
		}
		existingOwner := existing.Annotations["divergedev.com/tunnel-owner"]
		existingClientID := existing.Annotations["divergedev.com/tunnel-client-id"]

		unexpired := previousHolder != ""
		if unexpired && existing.Spec.RenewTime != nil && existing.Spec.LeaseDurationSeconds != nil {
			expiresAt := existing.Spec.RenewTime.Add(time.Duration(*existing.Spec.LeaseDurationSeconds) * time.Second)
			if nowTime.After(expiresAt) {
				unexpired = false
			}
		}

		if unexpired {
			var silentSameMachine bool
			if opts.Owner != "" {
				// Authenticated owners must present a matching, non-empty ClientInstanceID.
				silentSameMachine = existingOwner == opts.Owner &&
					existingClientID != "" &&
					existingClientID == opts.ClientInstanceID
			} else if existingOwner == "" {
				// Unauthenticated callers match only when both omit ClientInstanceID (legacy v2)
				// or both present identical non-empty ClientInstanceIDs.
				silentSameMachine = (existingClientID == "" && opts.ClientInstanceID == "") ||
					(existingClientID != "" && existingClientID == opts.ClientInstanceID)
			}
			if !silentSameMachine {
				if !opts.Force {
					return nil, fmt.Errorf("%w: held by owner=%q client=%q", ErrTunnelConflict, existingOwner, existingClientID)
				}
				if existingOwner != opts.Owner && opts.CheckCrossUserForce != nil {
					if authErr := opts.CheckCrossUserForce(existingOwner); authErr != nil {
						return nil, authErr
					}
				}
			}
		}

		var transitions int32
		if existing.Spec.LeaseTransitions != nil {
			transitions = *existing.Spec.LeaseTransitions
		}
		transitions++

		updatedObj := existing.DeepCopy()
		labels, annotations := buildTunnelLeaseMeta(opts, tl.podName, nowTime, transitions)
		if updatedObj.Labels == nil {
			updatedObj.Labels = make(map[string]string, len(labels))
		}
		for k, v := range labels {
			updatedObj.Labels[k] = v
		}
		if opts.Service == "" {
			delete(updatedObj.Labels, "divergedev.com/service")
		}

		updatedObj.Annotations = make(map[string]string, len(annotations))
		for k, v := range annotations {
			updatedObj.Annotations[k] = v
		}

		updatedObj.Spec.HolderIdentity = &holderIdentity
		updatedObj.Spec.AcquireTime = &now
		updatedObj.Spec.RenewTime = &now
		updatedObj.Spec.LeaseDurationSeconds = &leaseDuration
		updatedObj.Spec.LeaseTransitions = &transitions

		saved, updateErr := tl.k8sClient.CoordinationV1().Leases(opts.Namespace).Update(ctx, updatedObj, metav1.UpdateOptions{})
		if updateErr != nil {
			if (apierrors.IsConflict(updateErr) || apierrors.IsNotFound(updateErr)) && attempt < maxAcquireAttempts-1 {
				continue
			}
			return nil, fmt.Errorf("failed to update tunnel lease: %w", updateErr)
		}

		tl.logger.Info("tunnel lease acquired (taken over)", "lease", leaseName, "preview-id", opts.PreviewID, "previous", previousHolder)
		return &LeaseAcquireResult{
			LeaseName:        leaseName,
			LeaseUID:         saved.UID,
			HolderIdentity:   holderIdentity,
			LeaseTransitions: transitions,
			PreviousHolder:   previousHolder,
			PreviousOwner:    existingOwner,
			Evicted:          previousHolder != "",
		}, nil
	}

	return nil, fmt.Errorf("failed to acquire tunnel lease %q after %d attempts", leaseName, maxAcquireAttempts)
}

// Renew extends the Lease and distinguishes holder eviction from transient API errors.
func (tl *TunnelLease) Renew(ctx context.Context, namespace, leaseName, tunnelID string) (RenewResult, error) {
	if tl.k8sClient == nil {
		return RenewTransientError, fmt.Errorf("kubernetes client is nil")
	}
	holderIdentity := fmt.Sprintf("%s:%s", tl.podName, tunnelID)

	for attempt := 0; attempt < 2; attempt++ {
		existing, err := tl.k8sClient.CoordinationV1().Leases(namespace).Get(ctx, leaseName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				tl.logger.Info("lease not found during renewal", "lease", leaseName)
				return RenewEvicted, nil
			}
			tl.logger.Warn("failed to get lease for renewal", "lease", leaseName, "err", err)
			return RenewTransientError, err
		}

		if existing.Spec.HolderIdentity == nil || *existing.Spec.HolderIdentity != holderIdentity {
			tl.logger.Info("lease lost to another holder", "lease", leaseName)
			return RenewEvicted, nil
		}

		updatedObj := existing.DeepCopy()
		now := metav1.NewMicroTime(time.Now())
		updatedObj.Spec.RenewTime = &now
		if updatedObj.Annotations != nil {
			delete(updatedObj.Annotations, "divergedev.com/tunnel-expires")
		}

		_, err = tl.k8sClient.CoordinationV1().Leases(namespace).Update(ctx, updatedObj, metav1.UpdateOptions{})
		if err == nil {
			return RenewOK, nil
		}
		if apierrors.IsConflict(err) && attempt == 0 {
			continue
		}
		if apierrors.IsNotFound(err) {
			return RenewEvicted, nil
		}
		tl.logger.Warn("failed to renew lease", "lease", leaseName, "err", err)
		return RenewTransientError, err
	}

	return RenewTransientError, fmt.Errorf("lease renewal conflict exhausted")
}

// Release atomically deletes the Lease using UID + ResourceVersion preconditions
// only if we still hold it. Returns true if the Lease was deleted.
func (tl *TunnelLease) Release(ctx context.Context, namespace, leaseName, tunnelID string) bool {
	if tl.k8sClient == nil {
		return false
	}
	holderIdentity := fmt.Sprintf("%s:%s", tl.podName, tunnelID)

	existing, err := tl.k8sClient.CoordinationV1().Leases(namespace).Get(ctx, leaseName, metav1.GetOptions{})
	if err != nil {
		return false
	}
	if existing.Spec.HolderIdentity == nil || *existing.Spec.HolderIdentity != holderIdentity {
		return false
	}

	preconditions := &metav1.Preconditions{
		ResourceVersion: &existing.ResourceVersion,
	}
	if existing.UID != "" {
		uid := existing.UID
		preconditions.UID = &uid
	}

	err = tl.k8sClient.CoordinationV1().Leases(namespace).Delete(ctx, leaseName, metav1.DeleteOptions{
		Preconditions: preconditions,
	})
	if err != nil {
		if !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
			tl.logger.Warn("failed to release tunnel lease", "lease", leaseName, "err", err)
		}
		return false
	}
	return true
}

// IsCurrentHolder reports whether this pod and tunnelID still hold the named Lease.
func (tl *TunnelLease) IsCurrentHolder(ctx context.Context, namespace, leaseName, tunnelID string) (bool, error) {
	if tl.k8sClient == nil {
		return false, fmt.Errorf("kubernetes client is nil")
	}
	existing, err := tl.k8sClient.CoordinationV1().Leases(namespace).Get(ctx, leaseName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	holderIdentity := fmt.Sprintf("%s:%s", tl.podName, tunnelID)
	return existing.Spec.HolderIdentity != nil && *existing.Spec.HolderIdentity == holderIdentity, nil
}
