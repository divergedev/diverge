package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"k8s.io/apimachinery/pkg/runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestRunPostDeployJob_DefaultNonBlocking(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	c := fake.NewClientBuilder().WithScheme(s).Build()
	r := &PreviewGroupReconciler{Client: c}

	ctx := context.Background()

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	spec := &divergeiov1alpha1.PostDeploySpec{
		Image: "alpine",
	}

	err := r.runPostDeployJob(ctx, env, spec)
	require.NoError(t, err) // Non-blocking should return immediately

	hashInput := "alpine"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]
	jobName := generateHookJobName(env.Name, "postdeploy-"+hash)

	// Assert Job was created
	var job batchv1.Job
	err = c.Get(ctx, client.ObjectKey{Name: jobName, Namespace: "default"}, &job)
	require.NoError(t, err)

	assert.Equal(t, hookTypePostDeploy, job.Labels[labelHookType])

	// Assert NO database URL injected
	c0 := job.Spec.Template.Spec.Containers[0]
	for _, envVar := range c0.Env {
		assert.NotEqual(t, "DATABASE_URL", envVar.Name)
	}
}

func TestRunPostDeployJob_BlockingWait(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	c := fake.NewClientBuilder().WithScheme(s).Build()
	r := &PreviewGroupReconciler{Client: c}

	ctx := context.Background()

	tTrue := true
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}
	spec := &divergeiov1alpha1.PostDeploySpec{
		Image:    "alpine",
		Blocking: &tTrue,
	}

	err := r.runPostDeployJob(ctx, env, spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is still running")

	hashInput := "alpine"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]
	jobName := generateHookJobName(env.Name, "postdeploy-"+hash)

	var job batchv1.Job
	err = c.Get(ctx, client.ObjectKey{Name: jobName, Namespace: "default"}, &job)
	require.NoError(t, err)
	job.Status.Conditions = []batchv1.JobCondition{
		{
			Type:   batchv1.JobComplete,
			Status: corev1.ConditionTrue,
		},
	}
	err = c.Status().Update(ctx, &job)
	require.NoError(t, err)

	err = r.runPostDeployJob(ctx, env, spec)
	require.NoError(t, err)
}
