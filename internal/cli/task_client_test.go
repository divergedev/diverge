package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestMockTaskClient(t *testing.T) {
	ctx := context.Background()
	client := NewMockTaskClient()

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-mock-1",
			Namespace: "default",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Add payment gateway",
		},
	}

	// 1. Create
	err := client.Create(ctx, task)
	require.NoError(t, err)

	// 2. Duplicate create fails
	err = client.Create(ctx, task)
	assert.Error(t, err)

	// 3. Get
	fetched, err := client.Get(ctx, "default", "task-mock-1")
	require.NoError(t, err)
	assert.Equal(t, "Add payment gateway", fetched.Spec.Objective)

	// 4. List
	list, err := client.List(ctx, "default")
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// 5. Guide
	err = client.Guide(ctx, "default", "task-mock-1", "Use Stripe SDK")
	require.NoError(t, err)

	fetched, _ = client.Get(ctx, "default", "task-mock-1")
	assert.Equal(t, "Use Stripe SDK", fetched.Annotations[AnnotationGuidance])

	// 6. Pause
	err = client.Pause(ctx, "default", "task-mock-1")
	require.NoError(t, err)
	fetched, _ = client.Get(ctx, "default", "task-mock-1")
	assert.True(t, fetched.Spec.Suspended)

	// 7. Resume with bump
	err = client.Resume(ctx, "default", "task-mock-1", "15.00", 200000)
	require.NoError(t, err)
	fetched, _ = client.Get(ctx, "default", "task-mock-1")
	assert.False(t, fetched.Spec.Suspended)
	assert.Equal(t, "15.00", fetched.Spec.BudgetUSD)
	assert.Equal(t, int64(200000), fetched.Spec.BudgetTokens)

	// 8. Delete
	err = client.Delete(ctx, "default", "task-mock-1")
	require.NoError(t, err)
	_, err = client.Get(ctx, "default", "task-mock-1")
	assert.Error(t, err)
}

func TestKubeTaskClient(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	taskClient := NewKubeTaskClient(fakeClient)

	task := &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-kube-1",
			Namespace: "default",
		},
		Spec: v1alpha1.AgentTaskSpec{
			Objective: "Refactor auth",
		},
	}

	// 1. Create
	err := taskClient.Create(ctx, task)
	require.NoError(t, err)

	// 2. Get
	fetched, err := taskClient.Get(ctx, "default", "task-kube-1")
	require.NoError(t, err)
	assert.Equal(t, "Refactor auth", fetched.Spec.Objective)

	// 3. List
	items, err := taskClient.List(ctx, "default")
	require.NoError(t, err)
	assert.Len(t, items, 1)

	// 4. Guide
	err = taskClient.Guide(ctx, "default", "task-kube-1", "Use Macaroon tokens")
	require.NoError(t, err)
	fetched, _ = taskClient.Get(ctx, "default", "task-kube-1")
	assert.Equal(t, "Use Macaroon tokens", fetched.Annotations[AnnotationGuidance])

	// 5. Pause
	err = taskClient.Pause(ctx, "default", "task-kube-1")
	require.NoError(t, err)
	fetched, _ = taskClient.Get(ctx, "default", "task-kube-1")
	assert.True(t, fetched.Spec.Suspended)

	// 6. Resume
	err = taskClient.Resume(ctx, "default", "task-kube-1", "25.00", 500000)
	require.NoError(t, err)
	fetched, _ = taskClient.Get(ctx, "default", "task-kube-1")
	assert.False(t, fetched.Spec.Suspended)
	assert.Equal(t, "25.00", fetched.Spec.BudgetUSD)
	assert.Equal(t, int64(500000), fetched.Spec.BudgetTokens)

	// 7. Delete
	err = taskClient.Delete(ctx, "default", "task-kube-1")
	require.NoError(t, err)
	_, err = taskClient.Get(ctx, "default", "task-kube-1")
	assert.Error(t, err)

	// 8. Idempotent delete on non-existent task
	err = taskClient.Delete(ctx, "default", "non-existent-task")
	assert.NoError(t, err)
}

func TestNormalizeTaskBudgetArgAndAdditiveResume(t *testing.T) {
	assert.Equal(t, "5.00", normalizeTaskBudgetArg("+5.00"))
	assert.Equal(t, "2.50", normalizeTaskBudgetArg("+$2.50"))
	assert.Equal(t, "10.00", normalizeTaskBudgetArg("$10.00"))
	assert.Equal(t, "7.25", normalizeTaskBudgetArg(" + $7.25 "))

	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	for _, tc := range []struct {
		name   string
		client TaskClient
	}{
		{name: "mock", client: NewMockTaskClient()},
		{name: "kube", client: NewKubeTaskClient(fakeClient)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &v1alpha1.AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "task-budget-" + tc.name,
					Namespace: "default",
				},
				Spec: v1alpha1.AgentTaskSpec{
					Objective:     "Test additive resume budget and maxIterations bump",
					BudgetUSD:     "10.00",
					BudgetTokens:  100000,
					MaxIterations: 5,
					Suspended:     true,
				},
				Status: v1alpha1.AgentTaskStatus{
					Iteration: 5,
				},
			}
			require.NoError(t, tc.client.Create(ctx, task))

			// Additive resume with "+$5.00" and +50000 tokens
			require.NoError(t, tc.client.Resume(ctx, "default", task.Name, "+$5.00", 50000))
			fetched, err := tc.client.Get(ctx, "default", task.Name)
			require.NoError(t, err)
			assert.False(t, fetched.Spec.Suspended)
			assert.Equal(t, "15.00", fetched.Spec.BudgetUSD)
			assert.Equal(t, int64(150000), fetched.Spec.BudgetTokens)
			assert.Equal(t, int32(10), fetched.Spec.MaxIterations)

			// Second additive resume with "+2.50"
			require.NoError(t, tc.client.Resume(ctx, "default", task.Name, "+2.50", 0))
			fetched, err = tc.client.Get(ctx, "default", task.Name)
			require.NoError(t, err)
			assert.Equal(t, "17.50", fetched.Spec.BudgetUSD)
		})
	}
}
