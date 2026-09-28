package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestTaskLogsPodNotYetScheduled(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-task-logs",
			Namespace: "default",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Inspect logs",
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

	err := runTaskLogs(context.Background(), app, "test-task-logs", false, 50)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is not yet scheduled")
}
