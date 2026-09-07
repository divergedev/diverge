package doctor

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

// Severity indicates the urgency of a diagnosed issue.
type Severity string

const (
	SeverityInfo     Severity = "INFO"
	SeverityWarning  Severity = "WARNING"
	SeverityCritical Severity = "CRITICAL"
)

// Issue describes a detected cluster or workload problem and suggested remediation.
type Issue struct {
	Severity    Severity `json:"severity"`
	Component   string   `json:"component"`
	Summary     string   `json:"summary"`
	Details     string   `json:"details,omitempty"`
	Remediation string   `json:"remediation"`
}

// Report holds the complete diagnosis from a doctor run.
type Report struct {
	EnvironmentName string   `json:"environment_name,omitempty"`
	Namespace       string   `json:"namespace"`
	Healthy         bool     `json:"healthy"`
	Issues          []Issue  `json:"issues"`
	Suggestions     []string `json:"suggestions"`
}

// Diagnoser inspects cluster resources to find and explain errors.
type Diagnoser struct {
	client client.Client
}

// NewDiagnoser creates a new cluster diagnostician.
func NewDiagnoser(c client.Client) *Diagnoser {
	return &Diagnoser{client: c}
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

	// 1. Inspect Environment CRs if client is available
	if d.client != nil {
		if envName != "" {
			var env divergeiov1alpha1.Environment
			key := client.ObjectKey{Namespace: namespace, Name: envName}
			if err := d.client.Get(ctx, key, &env); err == nil {
				d.checkEnvironment(&env, report)
			}
		}

		// 2. Inspect Pods in the namespace
		var podList corev1.PodList
		listOpts := []client.ListOption{client.InNamespace(namespace)}
		if envName != "" {
			listOpts = append(listOpts, client.MatchingLabels{
				"diverge.io/environment": envName,
			})
		}

		if err := d.client.List(ctx, &podList, listOpts...); err == nil {
			for i := range podList.Items {
				d.checkPod(&podList.Items[i], report)
			}
		}
	}

	if len(report.Issues) > 0 {
		report.Healthy = false
	}

	return report, nil
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
			Severity:    SeverityCritical,
			Component:   fmt.Sprintf("Environment/%s", env.Name),
			Summary:     fmt.Sprintf("Environment phase is %s", phase),
			Details:     details,
			Remediation: "Check container logs, health check probes, and init container status.",
		})
	}

	if env.Status.MigrationStatus == "Failed" {
		report.Issues = append(report.Issues, Issue{
			Severity:    SeverityCritical,
			Component:   fmt.Sprintf("Environment/%s (Migration)", env.Name),
			Summary:     "Database migration hook failed",
			Remediation: "Inspect migration Job logs to fix SQL syntax or schema conflicts.",
		})
	}

	for _, cond := range env.Status.Conditions {
		if cond.Status == metav1.ConditionFalse {
			report.Issues = append(report.Issues, Issue{
				Severity:    SeverityWarning,
				Component:   fmt.Sprintf("EnvironmentCondition/%s", cond.Type),
				Summary:     fmt.Sprintf("Condition %s is False: %s", cond.Type, cond.Reason),
				Details:     cond.Message,
				Remediation: fmt.Sprintf("Verify that %s dependencies are healthy.", cond.Type),
			})
		}
	}
}

func (d *Diagnoser) checkPod(pod *corev1.Pod, report *Report) {
	// Check Phase
	if pod.Status.Phase == corev1.PodPending {
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
				report.Issues = append(report.Issues, Issue{
					Severity:    SeverityCritical,
					Component:   fmt.Sprintf("Pod/%s", pod.Name),
					Summary:     "Pod unschedulable (insufficient resources or node selector mismatch)",
					Details:     cond.Message,
					Remediation: "Verify node capacity or adjust CPU/memory requests in diverge.yaml.",
				})
			}
		}
	}

	// Check Init Container Statuses
	for _, cs := range pod.Status.InitContainerStatuses {
		d.checkContainerStatus(pod.Name, cs, true, report)
	}

	// Check App Container Statuses
	for _, cs := range pod.Status.ContainerStatuses {
		d.checkContainerStatus(pod.Name, cs, false, report)
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
				Severity:    SeverityCritical,
				Component:   fmt.Sprintf("%s/%s (%s)", tag, cs.Name, podName),
				Summary:     "Container is repeatedly crashing (CrashLoopBackOff)",
				Details:     msg,
				Remediation: "Inspect recent application logs with `diverge logs` or verify startup command & env variables.",
			})
		case "ImagePullBackOff", "ErrImagePull":
			report.Issues = append(report.Issues, Issue{
				Severity:    SeverityCritical,
				Component:   fmt.Sprintf("%s/%s (%s)", tag, cs.Name, podName),
				Summary:     "Container image pull failed",
				Details:     msg,
				Remediation: "Verify image repository, tag existence, and imagePullSecrets in the cluster.",
			})
		case "CreateContainerConfigError":
			report.Issues = append(report.Issues, Issue{
				Severity:    SeverityCritical,
				Component:   fmt.Sprintf("%s/%s (%s)", tag, cs.Name, podName),
				Summary:     "Container configuration error (missing ConfigMap or Secret)",
				Details:     msg,
				Remediation: "Check that all ConfigMaps and Secrets referenced by envFrom or env exist.",
			})
		}
	}

	// Check Terminated state
	if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
		term := cs.State.Terminated
		summary := fmt.Sprintf("Container terminated with exit code %d (Reason: %s)", term.ExitCode, term.Reason)
		remediation := "Check container logs to diagnose unhandled exceptions."

		if term.Reason == "OOMKilled" || strings.Contains(strings.ToLower(term.Message), "oom") {
			summary = "Container was terminated due to Out Of Memory (OOMKilled)"
			remediation = "Increase container memory limit in diverge.yaml."
		}

		report.Issues = append(report.Issues, Issue{
			Severity:    SeverityCritical,
			Component:   fmt.Sprintf("%s/%s (%s)", tag, cs.Name, podName),
			Summary:     summary,
			Details:     term.Message,
			Remediation: remediation,
		})
	}
}
