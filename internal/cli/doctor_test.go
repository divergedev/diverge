package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestDoctorCmd_Healthy(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	mockClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	app := &App{
		Namespace: "default",
		Client:    mockClient,
	}

	root := NewRootCmd(app)
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "my-env"})

	err := root.Execute()
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "No issues detected")
}

func TestDoctorCmd_FailingPod(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-crash",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.io/environment": "pr-99",
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "api",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "CrashLoopBackOff",
							Message: "back-off 10s restarting failed container=api",
						},
					},
				},
			},
		},
	}

	mockClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pod).
		Build()

	app := &App{
		Namespace: "default",
		Client:    mockClient,
	}

	root := NewRootCmd(app)
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{"doctor", "pr-99"})

	err := root.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "doctor detected 1 issue(s)")
	assert.Contains(t, stdout.String(), "CrashLoopBackOff")
	assert.Contains(t, stdout.String(), "diverge logs")
}

func TestDoctorCmd_JSONOutput(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	mockClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	app := &App{
		Namespace: "default",
		Client:    mockClient,
	}

	root := NewRootCmd(app)
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "my-env", "--json"})

	err := root.Execute()
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), `"healthy": true`)
	assert.Contains(t, stdout.String(), `"environment_name": "my-env"`)
}
