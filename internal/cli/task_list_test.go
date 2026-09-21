package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestTaskListCmd(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-task-1",
			Namespace: "default",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Implement metrics",
			Repository: v1alpha1.AgentTaskRepository{
				URL: "https://github.com/org/repo",
			},
		},
		Status: v1alpha1.AgentTaskStatus{
			Phase: v1alpha1.AgentTaskPhasePending,
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

	err := runTaskList(context.Background(), app, "table")
	require.NoError(t, err)

	err = runTaskList(context.Background(), app, "json")
	require.NoError(t, err)

	err = runTaskList(context.Background(), app, "yaml")
	require.NoError(t, err)
}
