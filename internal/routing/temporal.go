package routing

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

const (
	temporalProviderName = "temporal"
)

// TemporalProvider implements AsyncProvider for Temporal workflow routing.
// It creates a ConfigMap in the preview namespace containing the environment
// configuration that the Temporal SDK propagator reads at startup.
type TemporalProvider struct {
	Client client.Client
}

var _ AsyncProvider = (*TemporalProvider)(nil)

func (p *TemporalProvider) Name() string { return temporalProviderName }

func (p *TemporalProvider) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	if errs := validation.IsValidLabelValue(env.Name); len(errs) > 0 {
		return fmt.Errorf("invalid environment name %q: %v", env.Name, errs)
	}

	uidSuffix := string(env.UID)
	if uidSuffix == "" {
		uidSuffix = env.Name
	}

	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("diverge-temporal-%s", uidSuffix),
			Namespace: env.Namespace,
			Labels: map[string]string{
				"diverge.io/managed-by":  "diverge",
				"diverge.io/environment": env.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: v1alpha1.GroupVersion.String(),
					Kind:       "Environment",
					Name:       env.Name,
					UID:        env.UID,
				},
			},
		},
		Data: map[string]string{
			"diverge-env":       env.Name,
			"task-queue-suffix": env.Name, // Workers should use <queue>-<env>
		},
	}

	if err := p.Client.Patch(ctx, cm, client.Apply, client.FieldOwner("diverge-controller")); err != nil { //nolint:staticcheck // typed SSA requires applyconfigurations
		return fmt.Errorf("failed to apply temporal configmap: %w", err)
	}

	return nil
}

func (p *TemporalProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	if errs := validation.IsValidLabelValue(env.Name); len(errs) > 0 {
		return fmt.Errorf("invalid environment name %q: %v", env.Name, errs)
	}

	opts := []client.DeleteAllOfOption{
		client.InNamespace(env.Namespace),
		client.MatchingLabels{
			"diverge.io/managed-by":  "diverge",
			"diverge.io/environment": env.Name,
		},
	}

	cm := &corev1.ConfigMap{}
	if err := p.Client.DeleteAllOf(ctx, cm, opts...); err != nil {
		return fmt.Errorf("failed to delete temporal configmaps: %w", err)
	}

	return nil
}
