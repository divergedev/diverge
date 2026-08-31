package controller

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

var blockedEnvVars = map[string]bool{
	// Kubernetes internal
	"KUBERNETES_SERVICE_HOST": true,
	"KUBERNETES_SERVICE_PORT": true,
	"KUBECONFIG":              true,
	// System paths
	"HOME": true,
	"PATH": true,
	// Dynamic linker injection
	"LD_PRELOAD":            true,
	"LD_LIBRARY_PATH":       true,
	"DYLD_INSERT_LIBRARIES": true, // macOS
	// Language-specific path injection
	"PYTHONPATH":        true,
	"NODE_PATH":         true,
	"RUBYLIB":           true,
	"PERL5LIB":          true,
	"CLASSPATH":         true,
	"GEM_HOME":          true,
	"GOPATH":            true,
	"NODE_OPTIONS":      true,
	"JAVA_TOOL_OPTIONS": true,
}

func validateEnvVarMapping(mapping map[string]string) error {
	for k, v := range mapping {
		if blockedEnvVars[k] {
			return fmt.Errorf("env var key %q is restricted and cannot be used in mapping", k)
		}
		if blockedEnvVars[v] {
			return fmt.Errorf("env var %q is restricted and cannot be overridden", v)
		}
	}
	return nil
}

// H3: Use Status().Patch() instead of Update() to avoid 409 conflicts.
// statusBase must be a DeepCopy captured before any mutations to env.
func (r *EnvironmentReconciler) updateStatusWithRequeue(ctx context.Context, env *divergeiov1alpha1.Environment, statusBase *divergeiov1alpha1.Environment, err error, requeueAfter time.Duration) (ctrl.Result, error) {
	patch := client.MergeFrom(statusBase)
	if updateErr := r.Status().Patch(ctx, env, patch); updateErr != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", updateErr)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	if requeueAfter > 0 {
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	return ctrl.Result{}, nil
}

func derivePhase(conditions []metav1.Condition) divergeiov1alpha1.EnvironmentPhase {
	if len(conditions) == 0 {
		return divergeiov1alpha1.PhasePending
	}
	allReady := true
	for _, c := range conditions {
		switch c.Status {
		case metav1.ConditionFalse:
			return divergeiov1alpha1.PhaseFailed
		case metav1.ConditionTrue:
			// continue checking
		default:
			// ConditionUnknown or empty — still transitioning
			allReady = false
		}
	}
	if allReady {
		return divergeiov1alpha1.PhaseRunning
	}
	return divergeiov1alpha1.PhaseDeploying
}

func mergeEnvVars(existing, asyncVars []divergeiov1alpha1.EnvVar) ([]divergeiov1alpha1.EnvVar, error) {
	out := make([]divergeiov1alpha1.EnvVar, len(existing))
	copy(out, existing)

	for _, asyncVar := range asyncVars {
		conflict := false
		for _, e := range out {
			if e.Name == asyncVar.Name {
				if e.Value != asyncVar.Value {
					return nil, fmt.Errorf("env var conflict for %q: existing=%q vs async=%q", asyncVar.Name, e.Value, asyncVar.Value)
				}
				conflict = true
				break
			}
		}
		if !conflict {
			out = append(out, asyncVar)
		}
	}
	return out, nil
}
