package routing

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
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

// Name performs its designated operation.
func (p *TemporalProvider) Name() string { return temporalProviderName }

// Reconcile performs its designated operation.
func (p *TemporalProvider) Reconcile(ctx context.Context, env *v1alpha1.Environment) error {
	if errs := validation.IsValidLabelValue(env.Name); len(errs) > 0 {
		return fmt.Errorf("invalid environment name %q: %v", env.Name, errs)
	}

	uidSuffix := string(env.UID)
	if uidSuffix == "" {
		uidSuffix = env.Name
	}

	cmApply := corev1apply.ConfigMap(fmt.Sprintf("diverge-temporal-%s", uidSuffix), env.Namespace).
		WithLabels(map[string]string{
			"diverge.io/managed-by":  "diverge",
			"diverge.io/environment": env.Name,
		}).
		WithOwnerReferences(metav1apply.OwnerReference().
			WithAPIVersion(v1alpha1.GroupVersion.String()).
			WithKind("Environment").
			WithName(env.Name).
			WithUID(env.UID)).
		WithData(map[string]string{
			"diverge-env":       env.Name,
			"task-queue-suffix": env.Name, // Workers should use <queue>-<env>
		})

	if err := p.Client.Apply(ctx, cmApply, client.FieldOwner("diverge-controller"), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to apply temporal configmap: %w", err)
	}

	return nil
}

// Teardown performs its designated operation.
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
