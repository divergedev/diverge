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
	assert.Contains(t, report.Issues[0].Summary, "CrashLoopBackOff")
	assert.Contains(t, report.Issues[0].Remediation, "diverge logs")

	// Check OOM diagnosis
	assert.Contains(t, report.Issues[1].Summary, "OOMKilled")
	assert.Contains(t, report.Issues[1].Remediation, "memory limit")

	var buf bytes.Buffer
	err = FormatTable(&buf, report)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Detected Issues (2)")
	assert.Contains(t, buf.String(), "CRIT")
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
	// 1. Phase is Failed (CRIT)
	// 2. MigrationStatus is Failed (CRIT)
	// 3. Ready is False (WARN)
	// 4. DatabaseReady is False (WARN)
	assert.Len(t, report.Issues, 4)

	var jsonBuf bytes.Buffer
	err = FormatJSON(&jsonBuf, report)
	require.NoError(t, err)
	assert.Contains(t, jsonBuf.String(), "\"healthy\": false")
	assert.Contains(t, jsonBuf.String(), "Database migration hook failed")
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
	// 1. Pod unschedulable
	// 2. Init container CrashLoopBackOff
	// 3. ImagePullBackOff
	// 4. CreateContainerConfigError
	assert.Len(t, report.Issues, 4)

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
	assert.Equal(t, SeverityWarning, report.Issues[0].Severity)
	assert.Contains(t, report.Issues[0].Summary, "OOMKilled")
	assert.Contains(t, report.Issues[0].Summary, "previously terminated")
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
