package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func (r *EnvironmentReconciler) ensureNamespace(ctx context.Context, env *divergeiov1alpha1.Environment) error {
	if env.Spec.Deploy.Namespace == "create" {
		labels := map[string]string{
			"diverge.io/environment": env.Name,
			"diverge.io/managed-by":  "diverge",
		}
		// Merge user-defined labels; diverge.io/* labels take precedence
		for k, v := range env.Spec.Deploy.NamespaceLabels {
			if !strings.HasPrefix(k, "diverge.io/") {
				labels[k] = v
			}
		}

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: env.PreviewNamespace(),
			},
		}
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ns, func() error {
			ns.Labels = labels
			if ns.Labels == nil {
				ns.Labels = make(map[string]string)
			}
			ns.Labels["pod-security.kubernetes.io/enforce"] = "restricted"
			ns.Labels["pod-security.kubernetes.io/enforce-version"] = "latest"
			ns.Labels["pod-security.kubernetes.io/warn"] = "restricted"
			ns.Labels["pod-security.kubernetes.io/warn-version"] = "latest"
			ns.Labels["pod-security.kubernetes.io/audit"] = "restricted"
			ns.Labels["pod-security.kubernetes.io/audit-version"] = "latest"
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to create or update namespace: %w", err)
		}

		limitRange := &corev1.LimitRange{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "diverge-default-limits",
				Namespace: env.PreviewNamespace(),
				Labels: map[string]string{
					"diverge.io/managed-by": "diverge",
				},
			},
			Spec: corev1.LimitRangeSpec{
				Limits: []corev1.LimitRangeItem{{
					Type: corev1.LimitTypeContainer,
					Default: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("256Mi"),
						corev1.ResourceCPU:    resource.MustParse("250m"),
					},
					DefaultRequest: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("128Mi"),
						corev1.ResourceCPU:    resource.MustParse("100m"),
					},
				}},
			},
		}
		if err := r.Create(ctx, limitRange); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create limit range: %w", err)
		}

		quota := &corev1.ResourceQuota{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "diverge-preview-quota",
				Namespace: env.PreviewNamespace(),
				Labels: map[string]string{
					"diverge.io/managed-by": "diverge",
				},
			},
			Spec: corev1.ResourceQuotaSpec{
				Hard: corev1.ResourceList{
					corev1.ResourcePods:           resource.MustParse("5"),
					corev1.ResourceRequestsMemory: resource.MustParse("1Gi"),
					corev1.ResourceRequestsCPU:    resource.MustParse("1"),
					corev1.ResourceLimitsMemory:   resource.MustParse("2Gi"),
					corev1.ResourceLimitsCPU:      resource.MustParse("2"),
				},
			},
		}
		if err := r.Create(ctx, quota); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create resource quota: %w", err)
		}

		netpol := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "diverge-default-netpol",
				Namespace: env.PreviewNamespace(),
				Labels:    map[string]string{"diverge.io/managed-by": "diverge"},
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{}, // all pods
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
				Egress: []networkingv1.NetworkPolicyEgressRule{{
					To: []networkingv1.NetworkPolicyPeer{
						{
							IPBlock: &networkingv1.IPBlock{
								CIDR:   "0.0.0.0/0",
								Except: []string{"169.254.169.254/32"},
							},
						},
						{
							IPBlock: &networkingv1.IPBlock{
								CIDR:   "::/0",
								Except: []string{"fd00:ec2::254/128"},
							},
						},
					},
				}},
			},
		}
		if err := r.Create(ctx, netpol); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create network policy: %w", err)
		}
	}
	// "same" mode: namespace already exists (it's where the CR lives), nothing to do
	return nil
}
