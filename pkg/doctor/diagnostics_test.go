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
