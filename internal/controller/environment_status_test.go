package controller

import (
	"testing"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDerivePhase_AllReady(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: "A", Status: metav1.ConditionTrue},
		{Type: "B", Status: metav1.ConditionTrue},
	}
	phase := derivePhase(conditions)
	assert.Equal(t, divergeiov1alpha1.PhaseRunning, phase)
}

func TestDerivePhase_SomeFalse(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: "A", Status: metav1.ConditionTrue},
		{Type: "B", Status: metav1.ConditionFalse},
	}
	phase := derivePhase(conditions)
	assert.Equal(t, divergeiov1alpha1.PhaseFailed, phase)
}

func TestDerivePhase_NoConditions(t *testing.T) {
	phase := derivePhase([]metav1.Condition{})
	assert.Equal(t, divergeiov1alpha1.PhasePending, phase)
}

func TestMergeEnvVars_NoDuplicates(t *testing.T) {
	existing := []divergeiov1alpha1.EnvVar{
		{Name: "VAR1", Value: "val1"},
	}
	asyncVars := []divergeiov1alpha1.EnvVar{
		{Name: "VAR2", Value: "val2"},
	}

	res, err := mergeEnvVars(existing, asyncVars)
	assert.NoError(t, err)
	assert.Len(t, res, 2)
}

func TestMergeEnvVars_DuplicateKeys(t *testing.T) {
	existing := []divergeiov1alpha1.EnvVar{
		{Name: "VAR1", Value: "val1"},
	}
	asyncVars := []divergeiov1alpha1.EnvVar{
		{Name: "VAR1", Value: "val2"}, // conflict
	}

	res, err := mergeEnvVars(existing, asyncVars)
	assert.Error(t, err)
	assert.Nil(t, res)
}

func TestValidateEnvVarMapping_BlockedKey(t *testing.T) {
	mapping := map[string]string{
		"LD_PRELOAD": "myval",
	}
	err := validateEnvVarMapping(mapping)
	assert.Error(t, err)
}

func TestValidateEnvVarMapping_BlockedValue(t *testing.T) {
	mapping := map[string]string{
		"MY_VAR": "PATH",
	}
	err := validateEnvVarMapping(mapping)
	assert.Error(t, err)
}

func TestValidateEnvVarMapping_SafeVars(t *testing.T) {
	mapping := map[string]string{
		"CUSTOM_VAR_1": "CUSTOM_VAR_2",
	}
	err := validateEnvVarMapping(mapping)
	assert.NoError(t, err)
}
