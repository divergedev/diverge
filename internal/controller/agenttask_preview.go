package controller

import (
	"context"
	"fmt"

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
	annotationPreviewEnvironment = "divergedev.com/preview-environment"
	annotationPreviewURL         = "divergedev.com/preview-url"

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

func linkedPreviewEnvironmentName(taskName string) string {
	return sanitizeK8sName("agent-preview-"+taskName, taskName, 63)
}

func (r *AgentTaskReconciler) reconcileLinkedPreviewEnvironment(ctx context.Context, task *v1alpha1.AgentTask) error {
	if !isPreviewEnabledForTask(task) {
		return nil
	}

	envName := linkedPreviewEnvironmentName(task.Name)
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

		if r.Scheme != nil {
			if err := controllerutil.SetControllerReference(task, env, r.Scheme); err != nil {
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
		} else {
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

		env.Spec.Source.Branch = branch
		if env.Spec.ServiceConfig == nil {
			env.Spec.ServiceConfig = &v1alpha1.ServicePreviewConfig{}
		}
		env.Spec.ServiceConfig.ServiceName = serviceName
		env.Spec.ServiceConfig.Image = image
		if env.Spec.ServiceConfig.Port == 0 {
			env.Spec.ServiceConfig.Port = 8080
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
		_ = r.Patch(ctx, task, metaPatch)
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
	if env.Status.Phase == v1alpha1.PhaseRunning || env.Status.URL != "" {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "EnvironmentRunning"
		cond.Message = fmt.Sprintf("Linked preview environment %s is ready (%s)", envName, env.Status.URL)
	}
	meta.SetStatusCondition(&task.Status.Conditions, cond)

	return nil
}
