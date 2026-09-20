package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestTaskPauseAndResumeCmd(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-task-pause",
			Namespace: "default",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Refactor auth",
			Repository: v1alpha1.AgentTaskRepository{
				URL: "https://github.com/org/repo",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(task).
		WithStatusSubresource(&v1alpha1.AgentTask{}).
		Build()

	app := &App{
		Namespace: "default",
		Client:    fakeClient,
	}

	// 1. Pause
	err := runTaskSetSuspended(context.Background(), app, "test-task-pause", true)
	require.NoError(t, err)

	updated := &v1alpha1.AgentTask{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "test-task-pause"}, updated)
	require.NoError(t, err)
	assert.True(t, updated.Spec.Suspended)

	// 2. Resume
	err = runTaskSetSuspended(context.Background(), app, "test-task-pause", false)
	require.NoError(t, err)

	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "test-task-pause"}, updated)
	require.NoError(t, err)
	assert.False(t, updated.Spec.Suspended)
}
