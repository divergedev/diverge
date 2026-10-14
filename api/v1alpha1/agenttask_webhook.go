package v1alpha1

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// SetupAgentTaskWebhookWithManager registers the validating webhook for AgentTask.
func SetupAgentTaskWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &AgentTask{}).
		WithValidator(&agentTaskValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-divergedev-com-v1alpha1-agenttask,mutating=false,failurePolicy=fail,sideEffects=None,groups=divergedev.com,resources=agenttasks,verbs=create;update,versions=v1alpha1,name=vagenttask.divergedev.com,admissionReviewVersions=v1

// agentTaskValidator validates AgentTask resources.
type agentTaskValidator struct{}

var _ admission.Validator[*AgentTask] = &agentTaskValidator{}

// ValidateCreate validates a new AgentTask.
func (v *agentTaskValidator) ValidateCreate(_ context.Context, task *AgentTask) (admission.Warnings, error) {
	return nil, ValidateAgentTask(task).ToAggregate()
}

// ValidateUpdate validates an updated AgentTask.
func (v *agentTaskValidator) ValidateUpdate(_ context.Context, _ *AgentTask, newTask *AgentTask) (admission.Warnings, error) {
	return nil, ValidateAgentTask(newTask).ToAggregate()
}

// ValidateDelete is a no-op for deletion.
func (v *agentTaskValidator) ValidateDelete(_ context.Context, _ *AgentTask) (admission.Warnings, error) {
	return nil, nil
}

// ValidateAgentTask performs structural and security validation on an AgentTask.
func ValidateAgentTask(task *AgentTask) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	// Blocked namespace check
	if blockedNamespaces[task.Namespace] {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("metadata", "namespace"),
			fmt.Sprintf("namespace %q is blocked for agent tasks", task.Namespace),
		))
	}

	// Validate objective
	if strings.TrimSpace(task.Spec.Objective) == "" {
		allErrs = append(allErrs, field.Required(specPath.Child("objective"), "objective is required"))
	}

	// Validate repository URL
	repoURLPath := specPath.Child("repository", "url")
	repoURL := strings.TrimSpace(task.Spec.Repository.URL)
	if repoURL == "" {
		allErrs = append(allErrs, field.Required(repoURLPath, "repository URL is required"))
	} else if !isValidRepoURL(repoURL) {
		allErrs = append(allErrs, field.Invalid(
			repoURLPath,
			task.Spec.Repository.URL,
			"must start with https://, http://, ssh://, or git@",
		))
	}

	// Validate budgetUSD
	if task.Spec.BudgetUSD != "" {
		v, err := strconv.ParseFloat(task.Spec.BudgetUSD, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			allErrs = append(allErrs, field.Invalid(
				specPath.Child("budgetUSD"),
				task.Spec.BudgetUSD,
				"must be a positive decimal amount (e.g. \"5.00\")",
			))
		}
	}

	// Validate budgetTokens
	if task.Spec.BudgetTokens < 0 {
		allErrs = append(allErrs, field.Invalid(
			specPath.Child("budgetTokens"),
			task.Spec.BudgetTokens,
			"must be non-negative",
		))
	}

	// Validate maxIterations
	if task.Spec.MaxIterations < 0 {
		allErrs = append(allErrs, field.Invalid(
			specPath.Child("maxIterations"),
			task.Spec.MaxIterations,
			"must be non-negative",
		))
	}

	// Validate sandbox.timeoutSeconds
	if task.Spec.Sandbox.TimeoutSeconds < 0 {
		allErrs = append(allErrs, field.Invalid(
			specPath.Child("sandbox", "timeoutSeconds"),
			task.Spec.Sandbox.TimeoutSeconds,
			"must be non-negative",
		))
	}

	return allErrs
}

func isValidRepoURL(u string) bool {
	return strings.HasPrefix(u, "https://") ||
		strings.HasPrefix(u, "http://") ||
		strings.HasPrefix(u, "ssh://") ||
		strings.HasPrefix(u, "git@")
}
