package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/divergedev/diverge/api/v1alpha1"
)

const (
	annotationPreviewEnabled     = "divergedev.com/preview-enabled"
	annotationPreviewBranch      = "divergedev.com/preview-branch"
	annotationPreviewService     = "divergedev.com/preview-service"
	annotationPreviewImage       = "divergedev.com/preview-image"
	annotationPreviewPort        = "divergedev.com/preview-port"
	annotationPreviewEnvironment = "divergedev.com/preview-environment"
	annotationPreviewURL         = "divergedev.com/preview-url"
	annotationSandboxIP          = "divergedev.com/sandbox-ip"

	labelAgentTask           = "divergedev.com/agent-task"
	managedByAgentTask       = "diverge-agenttask"
	conditionPreviewEnvReady = "PreviewEnvironmentReady"
)

func isPreviewEnabledForTask(task *v1alpha1.AgentTask) bool {
	if task == nil {
		return false
	}
	if val, ok := task.Annotations[annotationPreviewEnabled]; ok {
		if val == "false" {
			return false
		}
		if val == "true" {
			return true
		}
	}
	return task.Spec.Repository.URL != ""
}

func resolveTaskPreviewBranch(task *v1alpha1.AgentTask) string {
	if task.Spec.Repository.WorkingBranch != "" {
		return task.Spec.Repository.WorkingBranch
	}
	if b := task.Annotations[annotationPreviewBranch]; b != "" {
		return b
	}
	if task.Spec.Repository.BaseBranch != "" {
		return task.Spec.Repository.BaseBranch
	}
	return "main"
}

func resolveTaskPreviewService(task *v1alpha1.AgentTask) string {
	if svc := task.Annotations[annotationPreviewService]; svc != "" {
		return svc
	}
	if task.Name != "" {
		return task.Name
	}
	return "agent-service"
}

func resolveTaskPreviewImage(task *v1alpha1.AgentTask) string {
	if img := task.Annotations[annotationPreviewImage]; img != "" {
		return img
	}
	return "nginx:alpine"
}

func resolveTaskPreviewPort(task *v1alpha1.AgentTask) int32 {
	if task == nil || task.Annotations == nil {
		return 0
	}
	raw := strings.TrimSpace(task.Annotations[annotationPreviewPort])
	if raw == "" {
		return 0
	}
	p, err := strconv.Atoi(raw)
	if err != nil || p < 1 || p > 65535 {
		return 0
	}
	return int32(p)
}

func linkedPreviewEnvironmentName(taskName string) string {
	return sanitizeK8sName("agent-preview-"+taskName, taskName, 63)
}

func isOwnedByAgentTask(env *v1alpha1.Environment, task *v1alpha1.AgentTask) bool {
	if env == nil || task == nil {
		return false
	}
	if env.Labels[labelManagedBy] == managedByAgentTask && env.Labels[labelAgentTask] == truncateLabel(task.Name) {
		return true
	}
	for _, ref := range env.OwnerReferences {
		if ref.Kind == "AgentTask" && ref.Name == task.Name {
			return true
		}
	}
	return false
}

func (r *AgentTaskReconciler) reconcileLinkedPreviewEnvironment(ctx context.Context, task *v1alpha1.AgentTask) error {
	if task == nil {
		return nil
	}

	envName := linkedPreviewEnvironmentName(task.Name)

	if !isPreviewEnabledForTask(task) {
		var existingEnv v1alpha1.Environment
		if err := r.Get(ctx, types.NamespacedName{Name: envName, Namespace: task.Namespace}, &existingEnv); err == nil {
			if isOwnedByAgentTask(&existingEnv, task) {
				if delErr := client.IgnoreNotFound(r.Delete(ctx, &existingEnv)); delErr != nil {
					return fmt.Errorf("failed to delete linked preview environment %s: %w", envName, delErr)
				}
			}
		} else if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to check linked preview environment %s: %w", envName, err)
		}

		if task.Annotations[annotationPreviewEnvironment] != "" || task.Annotations[annotationPreviewURL] != "" {
			metaPatch := client.MergeFrom(task.DeepCopy())
			delete(task.Annotations, annotationPreviewEnvironment)
			delete(task.Annotations, annotationPreviewURL)
			savedStatus := task.Status.DeepCopy()
			if err := r.Patch(ctx, task, metaPatch); err != nil {
				return fmt.Errorf("failed to clear task preview annotations: %w", err)
			}
			task.Status = *savedStatus
		}

		meta.RemoveStatusCondition(&task.Status.Conditions, conditionPreviewEnvReady)
		return nil
	}

	branch := resolveTaskPreviewBranch(task)
	serviceName := resolveTaskPreviewService(task)
	image := resolveTaskPreviewImage(task)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      envName,
			Namespace: task.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, env, func() error {
		if env.Labels == nil {
			env.Labels = make(map[string]string)
		}
		env.Labels[labelAgentTask] = truncateLabel(task.Name)
		env.Labels[labelManagedBy] = managedByAgentTask

		if task.Status.SandboxIP != "" {
			if env.Annotations == nil {
				env.Annotations = make(map[string]string)
			}
			env.Annotations[annotationSandboxIP] = task.Status.SandboxIP
		}

		if r.Scheme != nil {
			if err := controllerutil.SetControllerReference(task, env, r.Scheme); err != nil {
				return err
			}
		} else {
			for _, ref := range env.OwnerReferences {
				if ref.Controller != nil && *ref.Controller && (ref.Kind != "AgentTask" || ref.Name != task.Name) {
					return fmt.Errorf("environment %s is already owned by %s/%s", env.Name, ref.Kind, ref.Name)
				}
			}
			if task.UID != "" {
				isCtrl := true
				env.OwnerReferences = []metav1.OwnerReference{{
					APIVersion:         "divergedev.com/v1alpha1",
					Kind:               "AgentTask",
					Name:               task.Name,
					UID:                task.UID,
					Controller:         &isCtrl,
					BlockOwnerDeletion: &isCtrl,
				}}
			}
		}

		env.Spec.Source.Branch = branch
		if env.Spec.ServiceConfig == nil {
			env.Spec.ServiceConfig = &v1alpha1.ServicePreviewConfig{}
		}
		env.Spec.ServiceConfig.ServiceName = serviceName
		env.Spec.ServiceConfig.Image = image
		if env.Spec.ServiceConfig.Port == 0 {
			if customPort := resolveTaskPreviewPort(task); customPort > 0 {
				env.Spec.ServiceConfig.Port = customPort
			} else if task.Annotations[annotationPreviewImage] == "" {
				env.Spec.ServiceConfig.Port = 80
			} else {
				env.Spec.ServiceConfig.Port = 8080
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile linked preview environment %s: %w", envName, err)
	}

	// Re-fetch to inspect current Status (e.g., URL / Phase)
	_ = r.Get(ctx, types.NamespacedName{Name: envName, Namespace: task.Namespace}, env)

	// Record linked environment name and URL in task annotations
	needAnnoPatch := false
	metaPatch := client.MergeFrom(task.DeepCopy())
	if task.Annotations == nil {
		task.Annotations = make(map[string]string)
	}
	if task.Annotations[annotationPreviewEnvironment] != envName {
		task.Annotations[annotationPreviewEnvironment] = envName
		needAnnoPatch = true
	}
	if env.Status.URL != "" && task.Annotations[annotationPreviewURL] != env.Status.URL {
		task.Annotations[annotationPreviewURL] = env.Status.URL
		needAnnoPatch = true
	}
	if needAnnoPatch {
		savedStatus := task.Status.DeepCopy()
		if err := r.Patch(ctx, task, metaPatch); err != nil {
			return fmt.Errorf("failed to patch task preview annotations: %w", err)
		}
		task.Status = *savedStatus
	}

	cond := metav1.Condition{
		Type:               conditionPreviewEnvReady,
		ObservedGeneration: task.Generation,
		LastTransitionTime: metav1.Now(),
		Status:             metav1.ConditionFalse,
		Reason:             "EnvironmentProvisioning",
		Message:            fmt.Sprintf("Linked preview environment %s is provisioning", envName),
	}
	if env.Status.Phase == v1alpha1.PhaseRunning && env.Status.URL != "" {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "EnvironmentRunning"
		cond.Message = fmt.Sprintf("Linked preview environment %s is ready (%s)", envName, env.Status.URL)
	} else if env.Status.Phase == v1alpha1.PhaseFailed {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "EnvironmentFailed"
		cond.Message = fmt.Sprintf("Linked preview environment %s failed", envName)
	}
	meta.SetStatusCondition(&task.Status.Conditions, cond)

	return nil
}
