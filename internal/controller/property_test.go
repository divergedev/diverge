package controller

import (
	"testing"

	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
