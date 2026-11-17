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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/multicluster"
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

func TestRunPostDeployJob_RemoteSpokeExecutionAndCleanup(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	hubClient := fake.NewClientBuilder().WithScheme(s).Build()
	spokeClient := fake.NewClientBuilder().WithScheme(s).Build()

	r := &PreviewGroupReconciler{
		Client: hubClient,
		Scheme: s,
		ClusterClients: &multicluster.StaticResolver{
			Hub: &multicluster.Target{Name: "local", IsRemote: false, Client: hubClient},
			Spokes: map[string]*multicluster.Target{
				"spoke-west": {Name: "spoke-west", IsRemote: true, Client: spokeClient},
			},
		},
	}

	ctx := context.Background()
	tTrue := true
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pg-group-api-12345678",
			Namespace: "default",
			UID:       "hub-child-uid",
			Annotations: map[string]string{
				"divergedev.com/traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace: "same",
				ClusterRef: &divergeiov1alpha1.ClusterTargetRef{
					Name:                "spoke-west",
					KubeconfigSecretRef: &divergeiov1alpha1.SecretRef{Name: "west-secret"},
				},
			},
		},
	}
	spec := &divergeiov1alpha1.PostDeploySpec{
		Image:    "curlimages/curl:8.11.0",
		Args:     []string{"-fsSL", "http://orders-preview:8080/ready"},
		Blocking: &tTrue,
	}

	err := r.runPostDeployJob(ctx, env, spec)
	require.ErrorIs(t, err, ErrHookInProgress)

	hashInput := spec.Image + "-fsSLhttp://orders-preview:8080/ready"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]
	jobName := generateHookJobName(env.Name, "postdeploy-"+hash)

	// Job must be created on spoke, NOT on hub, and must omit cross-cluster OwnerReferences.
	assert.True(t, apierrors.IsNotFound(hubClient.Get(ctx, client.ObjectKey{Name: jobName, Namespace: "default"}, &batchv1.Job{})))

	var spokeJob batchv1.Job
	require.NoError(t, spokeClient.Get(ctx, client.ObjectKey{Name: jobName, Namespace: "default"}, &spokeJob))
	assert.Empty(t, spokeJob.OwnerReferences, "spoke post-deploy Job must not carry hub OwnerReference")

	// Mark spoke Job Complete and re-run blocking monitor.
	spokeJob.Status.Conditions = []batchv1.JobCondition{
		{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
	}
	require.NoError(t, spokeClient.Status().Update(ctx, &spokeJob))
	require.NoError(t, r.runPostDeployJob(ctx, env, spec))

	// Cleanup deletes the spoke Job.
	require.NoError(t, r.cleanupPostDeployJobs(ctx, env))
	assert.True(t, apierrors.IsNotFound(spokeClient.Get(ctx, client.ObjectKey{Name: jobName, Namespace: "default"}, &batchv1.Job{})))
}
