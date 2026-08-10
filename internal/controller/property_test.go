package controller

import (
	"testing"

	"context"
	"hegel.dev/go/hegel"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestDerivePhase(t *testing.T) {
	// Property: deriving phase from conditions is deterministic
	hegel.Test(t, func(ht *hegel.T) {
		// Simple deterministic generation for property test without advanced hegel functions
		statusList := []metav1.ConditionStatus{"True", "False", "Unknown"}
		conditions := []metav1.Condition{}

		// Use a simple draw from hegel text to introduce randomness
		seedStr := hegel.Draw(ht, hegel.Text())
		numCond := len(seedStr) % 5

		for i := 0; i < numCond; i++ {
			idx := (len(seedStr) + i) % 3
			conditions = append(conditions, metav1.Condition{
				Type:   "ConditionType",
				Status: statusList[idx],
			})
		}

		phase1 := derivePhase(conditions)
		phase2 := derivePhase(conditions)

		if phase1 != phase2 {
			ht.Fatalf("derivePhase is not deterministic")
		}

		// Property: phase is always one of the valid enum values
		validPhases := map[divergeiov1alpha1.EnvironmentPhase]bool{
			divergeiov1alpha1.PhasePending:   true,
			divergeiov1alpha1.PhaseDeploying: true,
			divergeiov1alpha1.PhaseRunning:   true,
			divergeiov1alpha1.PhaseFailed:    true,
		}
		if !validPhases[phase1] {
			ht.Fatalf("Invalid phase derived: %v", phase1)
		}

		// Property: if all conditions are True, phase should be Running (unless empty)
		allTrue := true
		anyFalse := false
		for _, c := range conditions {
			if c.Status != metav1.ConditionTrue {
				allTrue = false
			}
			if c.Status == metav1.ConditionFalse {
				anyFalse = true
			}
		}

		if len(conditions) > 0 {
			if allTrue && phase1 != divergeiov1alpha1.PhaseRunning {
				ht.Fatalf("Expected PhaseRunning when all conditions are True, got %v", phase1)
			}
			if anyFalse && phase1 != divergeiov1alpha1.PhaseFailed {
				ht.Fatalf("Expected PhaseFailed when any condition is False, got %v", phase1)
			}
			if !allTrue && !anyFalse && phase1 != divergeiov1alpha1.PhaseDeploying {
				ht.Fatalf("Expected PhaseDeploying when conditions are True/Unknown (no False), got %v", phase1)
			}
		} else {
			if phase1 != divergeiov1alpha1.PhasePending {
				ht.Fatalf("Expected PhasePending when conditions are empty, got %v", phase1)
			}
		}
	})
}

func TestEnsureNamespaceLabels(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		seedStr := hegel.Draw(ht, hegel.Text())
		labelCount := len(seedStr) % 5
		userLabels := make(map[string]string)
		for i := 0; i < labelCount; i++ {
			// Basic random strings for keys and values
			key := hegel.Draw(ht, hegel.Text())
			// Kubernetes label keys can't be empty and must follow some rules,
			// but for this unit test our controller doesn't validate them,
			// just passes them to the client. Let's make sure key is not empty to be safe if k8s fake client validates it.
			if key == "" {
				key = "k"
			}
			val := hegel.Draw(ht, hegel.Text())
			userLabels[key] = val
		}

		ctx := context.Background()
		client := fake.NewClientBuilder().Build()
		r := &EnvironmentReconciler{Client: client}

		env := &divergeiov1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-env", Namespace: "default"},
			Spec: divergeiov1alpha1.EnvironmentSpec{
				Deploy: divergeiov1alpha1.EnvironmentDeploy{
					Namespace:       "create",
					NamespaceLabels: userLabels,
				},
			},
		}

		err := r.ensureNamespace(ctx, env)
		if err != nil {
			ht.Fatalf("ensureNamespace failed: %v", err)
		}

		ns := &corev1.Namespace{}
		err = client.Get(ctx, types.NamespacedName{Name: env.PreviewNamespace()}, ns)
		if err != nil {
			ht.Fatalf("failed to get namespace: %v", err)
		}

		// Check diverge.io labels are preserved and correct
		if ns.Labels["diverge.io/environment"] != "test-env" {
			ht.Fatalf("expected diverge.io/environment=test-env")
		}
		if ns.Labels["diverge.io/managed-by"] != "diverge" {
			ht.Fatalf("expected diverge.io/managed-by=diverge")
		}

		// Check user labels are merged correctly, except diverge.io/* which should be dropped
		for k, v := range userLabels {
			if strings.HasPrefix(k, "diverge.io/") {
				if k == "diverge.io/environment" || k == "diverge.io/managed-by" {
					continue
				}
				if _, ok := ns.Labels[k]; ok {
					ht.Fatalf("user label %s should have been dropped", k)
				}
			} else {
				if ns.Labels[k] != v {
					ht.Fatalf("expected user label %s=%s, got %s", k, v, ns.Labels[k])
				}
			}
		}
	})
}
