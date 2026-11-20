package doctor

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/multicluster"
)

// Severity indicates the urgency of a diagnosed issue.
type Severity string

const (
	SeverityInfo     Severity = "INFO"
	SeverityWarning  Severity = "WARNING"
	SeverityCritical Severity = "CRITICAL"
)

// DocsBaseURL is the base URL for Diverge troubleshooting documentation.
const DocsBaseURL = "https://divergedev.com/guides/troubleshooting/"

// Diagnostic error codes for Diverge doctor issues.
const (
	CodeEnvironmentFailed       = "DIV-101"
	CodeMigrationFailed         = "DIV-102"
	CodeConditionFalse          = "DIV-103"
	CodePodUnschedulable        = "DIV-104"
	CodeCrashLoopBackOff        = "DIV-105"
	CodeImagePullBackOff        = "DIV-106"
	CodeContainerConfigError    = "DIV-107"
	CodeOOMKilled               = "DIV-108"
	CodeContainerExit           = "DIV-109"
	CodeAgentTaskFailure        = "DIV-110"
	CodeSpokeClusterUnreachable = "DIV-111"
)

// Issue describes a detected cluster or workload problem and suggested remediation.
type Issue struct {
	Code        string   `json:"code,omitempty"`
	Severity    Severity `json:"severity"`
	Component   string   `json:"component"`
	Summary     string   `json:"summary"`
	Details     string   `json:"details,omitempty"`
	Remediation string   `json:"remediation"`
	DocsURL     string   `json:"docs_url,omitempty"`
}

// Report holds the full cluster diagnostic summary.
type Report struct {
	EnvironmentName string   `json:"environment_name,omitempty"`
	Namespace       string   `json:"namespace"`
	Healthy         bool     `json:"healthy"`
	Issues          []Issue  `json:"issues"`
	Suggestions     []string `json:"suggestions"`
}

// Diagnoser inspects Kubernetes workloads and Diverge CRDs.
type Diagnoser struct {
	client          client.Client
	clusterResolver multicluster.Resolver
}

// NewDiagnoser creates a fresh Diagnoser instance.
func NewDiagnoser(c client.Client) *Diagnoser {
	return &Diagnoser{client: c}
}

// WithClusterResolver overrides multi-cluster spoke client resolution on the Diagnoser.
func (d *Diagnoser) WithClusterResolver(r multicluster.Resolver) *Diagnoser {
	d.clusterResolver = r
	return d
}

func (d *Diagnoser) resolveSpokeTarget(ctx context.Context, env *divergeiov1alpha1.Environment) (*multicluster.Target, error) {
	resolver := d.clusterResolver
	if resolver == nil && d.client != nil {
		resolver = multicluster.NewManager(d.client, nil, nil, d.client.Scheme())
	}
	if resolver == nil {
		return nil, fmt.Errorf("multi-cluster resolver is not configured")
	}
	target, err := resolver.Resolve(ctx, env)
	if err != nil {
		return nil, err
	}
	if target == nil || target.Client == nil {
		return nil, fmt.Errorf("resolved spoke cluster client is nil")
	}
	return target, nil
}

// Diagnose inspects the specified namespace (and optional environment) for failures.
func (d *Diagnoser) Diagnose(ctx context.Context, namespace, envName string) (*Report, error) {
	report := &Report{
		EnvironmentName: envName,
		Namespace:       namespace,
		Healthy:         true,
		Issues:          make([]Issue, 0),
		Suggestions:     make([]string, 0),
	}

	// 1. Inspect Environment CRs (or PreviewGroup child Environments) if client is available
	if d.client != nil {
		targetEnvs := make([]client.ObjectKey, 0, 1)
		seenTargetEnvs := make(map[string]struct{})
		resolvedEnvs := make(map[string]*divergeiov1alpha1.Environment)
		addTargetEnv := func(ns, name string) {
			if name == "" {
				return
			}
			if ns == "" {
				ns = namespace
			}
			k := ns + "/" + name
			if _, ok := seenTargetEnvs[k]; !ok {
				seenTargetEnvs[k] = struct{}{}
				targetEnvs = append(targetEnvs, client.ObjectKey{Namespace: ns, Name: name})
			}
		}
		recordResolvedEnv := func(env *divergeiov1alpha1.Environment) {
			if env == nil {
				return
			}
			ns := env.Namespace
			if ns == "" {
				ns = namespace
			}
			envCopy := env.DeepCopy()
			resolvedEnvs[ns+"/"+env.Name] = envCopy
		}

		if envName != "" {
			addTargetEnv(namespace, envName)
			var env divergeiov1alpha1.Environment
			key := client.ObjectKey{Namespace: namespace, Name: envName}
			if err := d.client.Get(ctx, key, &env); err != nil {
				if !apierrors.IsNotFound(err) {
					return nil, fmt.Errorf("failed to get environment %s: %w", envName, err)
				}
				if err := d.inspectPreviewGroupChildren(ctx, namespace, envName, report, addTargetEnv, recordResolvedEnv); err != nil {
					return nil, err
				}
			} else {
				recordResolvedEnv(&env)
				d.checkEnvironment(&env, report)
			}
		}

		// 2. Inspect Pods in the target namespace(s) (including federated spoke clusters)
		seenPods := make(map[string]struct{})
		if envName == "" {
			var podList corev1.PodList
			if err := d.client.List(ctx, &podList, client.InNamespace(namespace)); err != nil {
				return nil, fmt.Errorf("failed to list pods in namespace %s: %w", namespace, err)
			}
			for i := range podList.Items {
				d.checkPod(&podList.Items[i], report)
			}

			var envList divergeiov1alpha1.EnvironmentList
			if err := d.client.List(ctx, &envList, client.InNamespace(namespace)); err != nil {
				if !meta.IsNoMatchError(err) && !runtime.IsNotRegisteredError(err) {
					return nil, fmt.Errorf("failed to list environments in namespace %s: %w", namespace, err)
				}
			} else {
				for i := range envList.Items {
					if multicluster.IsRemoteEnv(&envList.Items[i]) {
						d.inspectSpokeEnvironmentPods(ctx, namespace, &envList.Items[i], seenPods, report)
					}
				}
			}
		} else {
			for _, target := range targetEnvs {
				envObj := resolvedEnvs[target.Namespace+"/"+target.Name]
				if envObj != nil && multicluster.IsRemoteEnv(envObj) {
					d.inspectSpokeEnvironmentPods(ctx, target.Namespace, envObj, seenPods, report)
					continue
				}

				podNS := target.Namespace
				if envObj != nil && envObj.Spec.Deploy.Namespace == "create" {
					podNS = envObj.PreviewNamespace()
				}

				podList, err := d.listEnvironmentPods(ctx, d.client, podNS, target.Name)
				if err != nil {
					return nil, err
				}
				if len(podList.Items) == 0 && podNS != target.Namespace {
					podList, err = d.listEnvironmentPods(ctx, d.client, target.Namespace, target.Name)
					if err != nil {
						return nil, err
					}
				}
				for i := range podList.Items {
					podKey := "local/" + podList.Items[i].Namespace + "/" + podList.Items[i].Name
					if _, exists := seenPods[podKey]; exists {
						continue
					}
					seenPods[podKey] = struct{}{}
					d.checkPod(&podList.Items[i], report)
				}
			}
		}

		// 3. Inspect AgentTasks in the namespace (if registered in scheme)
		var taskList divergeiov1alpha1.AgentTaskList
		if err := d.client.List(ctx, &taskList, client.InNamespace(namespace)); err != nil {
			if !meta.IsNoMatchError(err) && !runtime.IsNotRegisteredError(err) {
				return nil, fmt.Errorf("failed to list agent tasks in namespace %s: %w", namespace, err)
			}
		} else {
			for i := range taskList.Items {
				if envName != "" {
					linkedEnv := taskList.Items[i].Annotations["divergedev.com/preview-environment"]
					if linkedEnv != envName {
						if _, ok := seenTargetEnvs[namespace+"/"+linkedEnv]; !ok {
							continue
						}
					}
				}
				d.checkAgentTask(&taskList.Items[i], report)
			}
		}
	}

	if len(report.Issues) > 0 {
		report.Healthy = false
	}

	return report, nil
}

func (d *Diagnoser) listEnvironmentPods(ctx context.Context, c client.Client, podNS, envName string) (corev1.PodList, error) {
	var podList corev1.PodList
	listOpts := []client.ListOption{
		client.InNamespace(podNS),
		client.MatchingLabels{"divergedev.com/environment": envName},
	}
	if err := c.List(ctx, &podList, listOpts...); err != nil {
		return podList, fmt.Errorf("failed to list pods in namespace %s: %w", podNS, err)
	}
	if len(podList.Items) == 0 {
		legacyOpts := []client.ListOption{
			client.InNamespace(podNS),
			client.MatchingLabels{"diverge.io/environment": envName},
		}
		if err := c.List(ctx, &podList, legacyOpts...); err != nil {
			return podList, fmt.Errorf("failed to list pods in namespace %s: %w", podNS, err)
		}
	}
	return podList, nil
}

func (d *Diagnoser) inspectSpokeEnvironmentPods(
	ctx context.Context,
	fallbackNS string,
	env *divergeiov1alpha1.Environment,
	seenPods map[string]struct{},
	report *Report,
) {
	clusterName := multicluster.ClusterName(env)
	target, err := d.resolveSpokeTarget(ctx, env)
	if err != nil {
		report.Issues = append(report.Issues, Issue{
			Code:        CodeSpokeClusterUnreachable,
			Severity:    SeverityCritical,
			Component:   fmt.Sprintf("Environment/%s (Cluster/%s)", env.Name, clusterName),
			Summary:     fmt.Sprintf("Spoke cluster %q could not be resolved", clusterName),
			Details:     err.Error(),
			Remediation: "Verify spec.deploy.clusterRef.kubeconfigSecretRef exists in the same namespace, contains a valid HTTPS kubeconfig, and is reachable from the Diverge hub.",
			DocsURL:     DocsBaseURL + "#div-111-spoke-cluster-unreachable",
		})
		return
	}

	podNS := env.Namespace
	if podNS == "" {
		podNS = fallbackNS
	}
	if env.Spec.Deploy.Namespace == "create" {
		podNS = env.PreviewNamespace()
	}

	podList, err := d.listEnvironmentPods(ctx, target.Client, podNS, env.Name)
	if err != nil {
		report.Issues = append(report.Issues, Issue{
			Code:        CodeSpokeClusterUnreachable,
			Severity:    SeverityCritical,
			Component:   fmt.Sprintf("Environment/%s (Cluster/%s)", env.Name, clusterName),
			Summary:     fmt.Sprintf("Failed to query workloads on spoke cluster %q", clusterName),
			Details:     err.Error(),
			Remediation: "Verify spoke cluster API server reachability, TLS CA bundle, and RBAC permissions for the spoke kubeconfig Secret.",
			DocsURL:     DocsBaseURL + "#div-111-spoke-cluster-unreachable",
		})
		return
	}

	for i := range podList.Items {
		podKey := clusterName + "/" + podList.Items[i].Namespace + "/" + podList.Items[i].Name
		if _, exists := seenPods[podKey]; exists {
			continue
		}
		seenPods[podKey] = struct{}{}
		d.checkPodOnCluster(&podList.Items[i], clusterName, report)
	}
}

func (d *Diagnoser) inspectPreviewGroupChildren(
	ctx context.Context,
	namespace, pgName string,
	report *Report,
	addTargetEnv func(ns, name string),
	recordResolvedEnv func(env *divergeiov1alpha1.Environment),
) error {
	checkedEnvs := make(map[string]struct{})
	checkChildEnv := func(env *divergeiov1alpha1.Environment) {
		ns := env.Namespace
		if ns == "" {
			ns = namespace
		}
		k := ns + "/" + env.Name
		if _, ok := checkedEnvs[k]; ok {
			return
		}
		checkedEnvs[k] = struct{}{}
		addTargetEnv(ns, env.Name)
		if recordResolvedEnv != nil {
			recordResolvedEnv(env)
		}
		d.checkEnvironment(env, report)
	}

	var pg divergeiov1alpha1.PreviewGroup
	pgErr := d.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: pgName}, &pg)
	if pgErr != nil && apierrors.IsNotFound(pgErr) {
		pgErr = d.client.Get(ctx, client.ObjectKey{Name: pgName}, &pg)
	}
	if pgErr != nil && !apierrors.IsNotFound(pgErr) && !meta.IsNoMatchError(pgErr) && !runtime.IsNotRegisteredError(pgErr) {
		return fmt.Errorf("failed to get preview group %s: %w", pgName, pgErr)
	}
	if pgErr == nil {
		for _, svc := range pg.Status.Services {
			svcNS := svc.Namespace
			if svcNS == "" {
				svcNS = namespace
			}
			if svc.EnvironmentName != "" {
				addTargetEnv(svcNS, svc.EnvironmentName)
				var childEnv divergeiov1alpha1.Environment
				err := d.client.Get(ctx, client.ObjectKey{Namespace: svcNS, Name: svc.EnvironmentName}, &childEnv)
				if err == nil {
					checkChildEnv(&childEnv)
					continue
				}
				if !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to get child environment %s/%s: %w", svcNS, svc.EnvironmentName, err)
				}
			}
			if svc.Phase == divergeiov1alpha1.PhaseFailed {
				report.Issues = append(report.Issues, Issue{
					Code:        CodeEnvironmentFailed,
					Severity:    SeverityCritical,
					Component:   fmt.Sprintf("PreviewGroup/%s/%s", pg.Name, svc.Name),
					Summary:     fmt.Sprintf("PreviewGroup service %s phase is Failed", svc.Name),
					Details:     svc.Message,
					Remediation: "Check child Environment status, container logs, and post-deploy hook jobs.",
					DocsURL:     DocsBaseURL + "#div-101-environment-failed",
				})
			}
		}
	}

	pgLabels := []string{
		"divergedev.com/previewgroup",
		"divergedev.com/preview-group",
		"diverge.io/previewgroup",
		"diverge.io/preview-group",
	}
	for _, labelKey := range pgLabels {
		var envList divergeiov1alpha1.EnvironmentList
		err := d.client.List(
			ctx,
			&envList,
			client.InNamespace(namespace),
			client.MatchingLabels{labelKey: pgName},
		)
		if err != nil {
			return fmt.Errorf("failed to list preview group environments in namespace %s: %w", namespace, err)
		}
		for i := range envList.Items {
			checkChildEnv(&envList.Items[i])
		}
	}

	return nil
}

func (d *Diagnoser) checkEnvironment(env *divergeiov1alpha1.Environment, report *Report) {
	phase := string(env.Status.Phase)
	if phase == "Failed" || phase == "Error" {
		details := ""
		for _, cond := range env.Status.Conditions {
			if cond.Status == metav1.ConditionFalse {
				details = cond.Message
				break
			}
		}
		report.Issues = append(report.Issues, Issue{
			Code:        CodeEnvironmentFailed,
			Severity:    SeverityCritical,
			Component:   fmt.Sprintf("Environment/%s", env.Name),
			Summary:     fmt.Sprintf("Environment phase is %s", phase),
			Details:     details,
			Remediation: "Check container logs, health check probes, and init container status.",
			DocsURL:     DocsBaseURL + "#div-101-environment-failed",
		})
	}

	if env.Status.MigrationStatus == "Failed" {
		report.Issues = append(report.Issues, Issue{
			Code:        CodeMigrationFailed,
			Severity:    SeverityCritical,
			Component:   fmt.Sprintf("Environment/%s (Migration)", env.Name),
			Summary:     "Database migration hook failed",
			Remediation: "Inspect migration Job logs to fix SQL syntax or schema conflicts.",
			DocsURL:     DocsBaseURL + "#div-102-migration-failed",
		})
	}

	for _, cond := range env.Status.Conditions {
		if cond.Status == metav1.ConditionFalse {
			report.Issues = append(report.Issues, Issue{
				Code:        CodeConditionFalse,
				Severity:    SeverityWarning,
				Component:   fmt.Sprintf("EnvironmentCondition/%s", cond.Type),
				Summary:     fmt.Sprintf("Condition %s is False: %s", cond.Type, cond.Reason),
				Details:     cond.Message,
				Remediation: fmt.Sprintf("Verify that %s dependencies are healthy.", cond.Type),
				DocsURL:     DocsBaseURL + "#div-103-condition-false",
			})
		}
	}
}

func (d *Diagnoser) checkPod(pod *corev1.Pod, report *Report) {
	d.checkPodOnCluster(pod, "", report)
}

func (d *Diagnoser) checkPodOnCluster(pod *corev1.Pod, clusterName string, report *Report) {
	podComponent := fmt.Sprintf("Pod/%s", pod.Name)
	podLabel := pod.Name
	if clusterName != "" && clusterName != multicluster.LocalClusterName {
		podComponent = fmt.Sprintf("Cluster/%s/Pod/%s", clusterName, pod.Name)
		podLabel = fmt.Sprintf("%s@%s", pod.Name, clusterName)
	}

	// Check Phase
	if pod.Status.Phase == corev1.PodPending {
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
				report.Issues = append(report.Issues, Issue{
					Code:        CodePodUnschedulable,
					Severity:    SeverityCritical,
					Component:   podComponent,
					Summary:     "Pod unschedulable (insufficient resources or node selector mismatch)",
					Details:     cond.Message,
					Remediation: "Verify node capacity or adjust CPU/memory requests in diverge.yaml.",
					DocsURL:     DocsBaseURL + "#div-104-pod-unschedulable",
				})
			}
		}
	}

	// Check Init Container Statuses
	for _, cs := range pod.Status.InitContainerStatuses {
		d.checkContainerStatus(podLabel, cs, true, report)
	}

	// Check App Container Statuses
	for _, cs := range pod.Status.ContainerStatuses {
		d.checkContainerStatus(podLabel, cs, false, report)
	}
}

func (d *Diagnoser) checkContainerStatus(podName string, cs corev1.ContainerStatus, isInit bool, report *Report) {
	tag := "Container"
	if isInit {
		tag = "InitContainer"
	}

	// Check Waiting state
	if cs.State.Waiting != nil {
		reason := cs.State.Waiting.Reason
		msg := cs.State.Waiting.Message

		switch reason {
		case "CrashLoopBackOff":
			report.Issues = append(report.Issues, Issue{
				Code:        CodeCrashLoopBackOff,
				Severity:    SeverityCritical,
				Component:   fmt.Sprintf("%s/%s (%s)", tag, cs.Name, podName),
				Summary:     "Container is repeatedly crashing (CrashLoopBackOff)",
				Details:     msg,
				Remediation: "Inspect recent application logs with `diverge logs` or verify startup command & env variables.",
				DocsURL:     DocsBaseURL + "#div-105-crashloopbackoff",
			})
		case "ImagePullBackOff", "ErrImagePull":
			report.Issues = append(report.Issues, Issue{
				Code:        CodeImagePullBackOff,
				Severity:    SeverityCritical,
				Component:   fmt.Sprintf("%s/%s (%s)", tag, cs.Name, podName),
				Summary:     "Container image pull failed",
				Details:     msg,
				Remediation: "Verify image repository, tag existence, and imagePullSecrets in the cluster.",
				DocsURL:     DocsBaseURL + "#div-106-imagepullbackoff",
			})
		case "CreateContainerConfigError":
			report.Issues = append(report.Issues, Issue{
				Code:        CodeContainerConfigError,
				Severity:    SeverityCritical,
				Component:   fmt.Sprintf("%s/%s (%s)", tag, cs.Name, podName),
				Summary:     "Container configuration error (missing ConfigMap or Secret)",
				Details:     msg,
				Remediation: "Check that all ConfigMaps and Secrets referenced by envFrom or env exist.",
				DocsURL:     DocsBaseURL + "#div-107-container-config-error",
			})
		}
	}

	// Check Terminated state
	if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
		term := cs.State.Terminated
		code := CodeContainerExit
		docsURL := DocsBaseURL + "#div-109-container-exit"
		summary := fmt.Sprintf("Container terminated with exit code %d (Reason: %s)", term.ExitCode, term.Reason)
		remediation := "Check container logs to diagnose unhandled exceptions."

		if term.Reason == "OOMKilled" || strings.Contains(strings.ToLower(term.Message), "oom") {
			code = CodeOOMKilled
			docsURL = DocsBaseURL + "#div-108-oomkilled"
			summary = "Container was terminated due to Out Of Memory (OOMKilled)"
			remediation = "Increase container memory limit in diverge.yaml."
		}

		report.Issues = append(report.Issues, Issue{
			Code:        code,
			Severity:    SeverityCritical,
			Component:   fmt.Sprintf("%s/%s (%s)", tag, cs.Name, podName),
			Summary:     summary,
			Details:     term.Message,
			Remediation: remediation,
			DocsURL:     docsURL,
		})
	}

	// Check LastTerminationState (previous crashes before restart)
	if cs.State.Waiting == nil && cs.State.Terminated == nil && cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.ExitCode != 0 {
		term := cs.LastTerminationState.Terminated
		code := CodeContainerExit
		docsURL := DocsBaseURL + "#div-109-container-exit"
		summary := fmt.Sprintf("Container previously terminated with exit code %d (Reason: %s)", term.ExitCode, term.Reason)
		remediation := "Check container logs to diagnose unhandled exceptions."

		if term.Reason == "OOMKilled" || strings.Contains(strings.ToLower(term.Message), "oom") {
			code = CodeOOMKilled
			docsURL = DocsBaseURL + "#div-108-oomkilled"
			summary = "Container was previously terminated due to Out Of Memory (OOMKilled)"
			remediation = "Increase container memory limit in diverge.yaml."
		}

		report.Issues = append(report.Issues, Issue{
			Code:        code,
			Severity:    SeverityWarning,
			Component:   fmt.Sprintf("%s/%s (%s)", tag, cs.Name, podName),
			Summary:     summary,
			Details:     term.Message,
			Remediation: remediation,
			DocsURL:     docsURL,
		})
	}
}

func (d *Diagnoser) checkAgentTask(task *divergeiov1alpha1.AgentTask, report *Report) {
	if task.Status.Phase == divergeiov1alpha1.AgentTaskPhaseFailed {
		report.Issues = append(report.Issues, Issue{
			Code:        CodeAgentTaskFailure,
			Severity:    SeverityCritical,
			Component:   fmt.Sprintf("AgentTask/%s", task.Name),
			Summary:     "AgentTask execution failed",
			Details:     task.Status.Message,
			Remediation: fmt.Sprintf("Inspect sandbox logs with `diverge task logs %s` or check task status with `diverge task status %s`.", task.Name, task.Name),
			DocsURL:     DocsBaseURL + "#div-110-agenttask-budget-or-failure",
		})
		return
	}

	if task.Status.Phase == divergeiov1alpha1.AgentTaskPhasePaused {
		budgetExceeded := strings.Contains(strings.ToLower(task.Status.Message), "budget")
		details := task.Status.Message
		for _, cond := range task.Status.Conditions {
			if (cond.Type == "BudgetExceeded" || cond.Reason == "BudgetExceeded") && cond.Status == metav1.ConditionTrue {
				budgetExceeded = true
				if cond.Message != "" {
					details = cond.Message
				}
				break
			}
		}
		if budgetExceeded {
			report.Issues = append(report.Issues, Issue{
				Code:        CodeAgentTaskFailure,
				Severity:    SeverityWarning,
				Component:   fmt.Sprintf("AgentTask/%s", task.Name),
				Summary:     "AgentTask paused due to budget exceeded",
				Details:     details,
				Remediation: fmt.Sprintf("Resume the task with a higher budget using `diverge task resume %s --budget <USD>`.", task.Name),
				DocsURL:     DocsBaseURL + "#div-110-agenttask-budget-or-failure",
			})
		}
	}
}
