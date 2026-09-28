package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestTaskGuideExecution(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-task-guide",
			Namespace: "default",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Refactor database migrations",
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

	guidanceMsg := "Focus on PostgreSQL 16 compatibility and avoid dropping tables"
	err := runTaskGuide(context.Background(), app, "test-task-guide", guidanceMsg)
	require.NoError(t, err)

	updated := &v1alpha1.AgentTask{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-task-guide"}, updated)
	require.NoError(t, err)

	assert.NotNil(t, updated.Annotations)
	assert.Equal(t, guidanceMsg, updated.Annotations[AnnotationGuidance])
	assert.NotEmpty(t, updated.Annotations[AnnotationGuidanceTimestamp])
}

func TestKubeTaskGuiderNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	guider := NewKubeTaskGuider(fakeClient)
	err := guider.Guide(context.Background(), "default", "non-existent-task", "hello")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get AgentTask")
}
