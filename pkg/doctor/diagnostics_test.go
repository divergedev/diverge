package doctor

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestDoctor_Healthy(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	diagnoser := NewDiagnoser(client)

	report, err := diagnoser.Diagnose(context.Background(), "default", "my-env")
	require.NoError(t, err)
	assert.True(t, report.Healthy)
	assert.Empty(t, report.Issues)

	var buf bytes.Buffer
	err = FormatTable(&buf, report)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "No issues detected")
}

func TestDoctor_CrashLoopAndOOM(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	podCrash := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-crash",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.io/environment": "pr-1",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "web",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "CrashLoopBackOff",
							Message: "back-off 5m0s restarting failed container=web",
						},
					},
				},
			},
		},
	}

	podOOM := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "worker-oom",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.io/environment": "pr-1",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "worker",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 137,
							Reason:   "OOMKilled",
							Message:  "command terminated by OOM killer",
						},
					},
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(podCrash, podOOM).
		Build()

	diagnoser := NewDiagnoser(client)
	report, err := diagnoser.Diagnose(context.Background(), "default", "pr-1")
	require.NoError(t, err)
	assert.False(t, report.Healthy)
	assert.Len(t, report.Issues, 2)

	// Check CrashLoop diagnosis
	assert.Equal(t, CodeCrashLoopBackOff, report.Issues[0].Code)
	assert.Equal(t, DocsBaseURL+"#div-105-crashloopbackoff", report.Issues[0].DocsURL)
	assert.Contains(t, report.Issues[0].Summary, "CrashLoopBackOff")
	assert.Contains(t, report.Issues[0].Remediation, "diverge logs")

	// Check OOM diagnosis
	assert.Equal(t, CodeOOMKilled, report.Issues[1].Code)
	assert.Equal(t, DocsBaseURL+"#div-108-oomkilled", report.Issues[1].DocsURL)
	assert.Contains(t, report.Issues[1].Summary, "OOMKilled")
	assert.Contains(t, report.Issues[1].Remediation, "memory limit")

	var buf bytes.Buffer
	err = FormatTable(&buf, report)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Detected Issues (2)")
	assert.Contains(t, buf.String(), "CRIT [DIV-105]")
	assert.Contains(t, buf.String(), "Docs:        🔗 https://divergedev.com/guides/troubleshooting/#div-105-crashloopbackoff")
}

func TestDoctor_EnvironmentDegradedAndMigrationFailed(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-failed",
			Namespace: "default",
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase:           divergeiov1alpha1.PhaseFailed,
			MigrationStatus: "Failed",
			Conditions: []metav1.Condition{
				{
					Type:    "Ready",
					Status:  metav1.ConditionFalse,
					Reason:  "DeploymentFailed",
					Message: "Workload replicas did not become healthy within timeout",
				},
				{
					Type:    "DatabaseReady",
					Status:  metav1.ConditionFalse,
					Reason:  "MigrationError",
					Message: "Failed to apply schema migration 003_add_index.sql",
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(env).
		Build()

	diagnoser := NewDiagnoser(client)
	report, err := diagnoser.Diagnose(context.Background(), "default", "pr-failed")
	require.NoError(t, err)
	assert.False(t, report.Healthy)

	// Issues expected:
	// 1. Phase is Failed (CRIT, DIV-101)
	// 2. MigrationStatus is Failed (CRIT, DIV-102)
	// 3. Ready is False (WARN, DIV-103)
	// 4. DatabaseReady is False (WARN, DIV-103)
	assert.Len(t, report.Issues, 4)
	assert.Equal(t, CodeEnvironmentFailed, report.Issues[0].Code)
	assert.Equal(t, CodeMigrationFailed, report.Issues[1].Code)
	assert.Equal(t, CodeConditionFalse, report.Issues[2].Code)
	assert.Equal(t, CodeConditionFalse, report.Issues[3].Code)

	var jsonBuf bytes.Buffer
	err = FormatJSON(&jsonBuf, report)
	require.NoError(t, err)
	assert.Contains(t, jsonBuf.String(), "\"healthy\": false")
	assert.Contains(t, jsonBuf.String(), "Database migration hook failed")
	assert.Contains(t, jsonBuf.String(), "\"code\": \"DIV-101\"")
}

func TestDoctor_PodUnschedulableAndInitContainers(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	podPending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-pending",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.io/environment": "pr-stuck",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{
				{
					Type:    corev1.PodScheduled,
					Status:  corev1.ConditionFalse,
					Reason:  corev1.PodReasonUnschedulable,
					Message: "0/5 nodes are available: 5 Insufficient cpu",
				},
			},
		},
	}

	podInitFail := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-init-fail",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.io/environment": "pr-stuck",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "migration-init",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "CrashLoopBackOff",
							Message: "migration init failed with exit code 1",
						},
					},
				},
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "api",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "ImagePullBackOff",
							Message: "Back-off pulling image registry.internal/app:nonexistent",
						},
					},
				},
				{
					Name: "sidecar",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "CreateContainerConfigError",
							Message: "secret \"db-credentials\" not found",
						},
					},
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(podPending, podInitFail).
		Build()

	diagnoser := NewDiagnoser(client)
	report, err := diagnoser.Diagnose(context.Background(), "default", "pr-stuck")
	require.NoError(t, err)
	assert.False(t, report.Healthy)

	// Issues:
	// - Pod unschedulable (DIV-104)
	// - Init container CrashLoopBackOff (DIV-105)
	// - ImagePullBackOff (DIV-106)
	// - CreateContainerConfigError (DIV-107)
	assert.Len(t, report.Issues, 4)
	var codes []string
	for _, iss := range report.Issues {
		codes = append(codes, iss.Code)
	}
	assert.ElementsMatch(t, []string{
		CodePodUnschedulable,
		CodeCrashLoopBackOff,
		CodeImagePullBackOff,
		CodeContainerConfigError,
	}, codes)

	var buf bytes.Buffer
	err = FormatTable(&buf, report)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Pod unschedulable")
	assert.Contains(t, buf.String(), "Container image pull failed")
	assert.Contains(t, buf.String(), "Container configuration error")
}

func TestDoctor_RestartedContainerPreviousOOM(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	podRestarted := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-restarted",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.io/environment": "pr-oom-restarted",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "api",
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 137,
							Reason:   "OOMKilled",
							Message:  "oom killer terminated container",
						},
					},
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(podRestarted).
		Build()

	diagnoser := NewDiagnoser(client)
	report, err := diagnoser.Diagnose(context.Background(), "default", "pr-oom-restarted")
	require.NoError(t, err)
	assert.False(t, report.Healthy)
	require.Len(t, report.Issues, 1)
	assert.Equal(t, CodeOOMKilled, report.Issues[0].Code)
	assert.Equal(t, SeverityWarning, report.Issues[0].Severity)
	assert.Contains(t, report.Issues[0].Summary, "OOMKilled")
	assert.Contains(t, report.Issues[0].Summary, "previously terminated")
}

func TestDoctor_ContainerNonZeroExitAndAgentTasks(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	podExited := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "job-exit",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "runner",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 2,
							Reason:   "Error",
							Message:  "panic: nil pointer dereference",
						},
					},
				},
			},
		},
	}

	failedTask := &divergeiov1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-broken",
			Namespace: "default",
		},
		Status: divergeiov1alpha1.AgentTaskStatus{
			Phase:   divergeiov1alpha1.AgentTaskPhaseFailed,
			Message: "max iterations exceeded without tests passing",
		},
	}

	pausedBudgetTask := &divergeiov1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-budget",
			Namespace: "default",
		},
		Status: divergeiov1alpha1.AgentTaskStatus{
			Phase: divergeiov1alpha1.AgentTaskPhasePaused,
			Conditions: []metav1.Condition{
				{
					Type:    "BudgetExceeded",
					Status:  metav1.ConditionTrue,
					Reason:  "BudgetExceeded",
					Message: "Spend $5.10 exceeded budget $5.00",
				},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(podExited, failedTask, pausedBudgetTask).
		Build()

	diagnoser := NewDiagnoser(c)
	report, err := diagnoser.Diagnose(context.Background(), "default", "")
	require.NoError(t, err)
	assert.False(t, report.Healthy)
	require.Len(t, report.Issues, 3)

	assert.Equal(t, CodeContainerExit, report.Issues[0].Code)
	assert.Equal(t, DocsBaseURL+"#div-109-container-exit", report.Issues[0].DocsURL)

	assert.Equal(t, CodeAgentTaskFailure, report.Issues[1].Code)
	assert.Equal(t, SeverityCritical, report.Issues[1].Severity)
	assert.Contains(t, report.Issues[1].Component, "AgentTask/")

	assert.Equal(t, CodeAgentTaskFailure, report.Issues[2].Code)
	assert.Equal(t, SeverityWarning, report.Issues[2].Severity)
	assert.Contains(t, report.Issues[2].Summary, "budget exceeded")
}

type errorClient struct {
	client.Client
	err error
}

func (e *errorClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return e.err
}

func (e *errorClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return e.err
}

func TestDoctor_APIErrors(t *testing.T) {
	diagnoserGetErr := NewDiagnoser(&errorClient{err: assert.AnError})
	_, err := diagnoserGetErr.Diagnose(context.Background(), "default", "my-env")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get environment")

	diagnoserListErr := NewDiagnoser(&errorClient{err: assert.AnError})
	_, err = diagnoserListErr.Diagnose(context.Background(), "default", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list pods")
}
