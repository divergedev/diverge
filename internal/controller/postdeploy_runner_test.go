package controller

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sevents "k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/events"
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

func TestPreviewGroupReconcile_OrphanHookCleanupFailureTriggersRetry(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	failSecondJobDelete := true
	deletedJobCount := 0

	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pg-orphan",
			Finalizers: []string{previewGroupFinalizer},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source:  divergeiov1alpha1.EnvironmentSource{Provider: "github", Branch: "feat/x"},
			Routing: divergeiov1alpha1.PreviewGroupRouting{HeaderValue: "pr-1"},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Image: "api:v1"},
			},
		},
	}

	orphanEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childEnvironmentName("pg-orphan", "removed-svc"),
			Namespace: "default",
			Labels: map[string]string{
				labelPreviewGroup: "pg-orphan",
				labelManagedBy:    "diverge-previewgroup",
			},
		},
	}
	job1 := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hook-orphan-1",
			Namespace: "default",
			Labels: map[string]string{
				labelEnvironment: truncateLabel(orphanEnv.Name),
				labelHookType:    hookTypePostDeploy,
			},
		},
	}
	job2 := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hook-orphan-2",
			Namespace: "default",
			Labels: map[string]string{
				labelEnvironment: truncateLabel(orphanEnv.Name),
				labelHookType:    hookTypePostDeploy,
			},
		},
	}

	hubClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(pg).
		WithObjects(pg, orphanEnv, job1, job2).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if _, isJob := obj.(*batchv1.Job); isJob {
					deletedJobCount++
					if failSecondJobDelete && deletedJobCount == 2 {
						return errors.New("transient spoke job delete failure")
					}
				}
				return cl.Delete(ctx, obj, opts...)
			},
		}).
		Build()

	r := &PreviewGroupReconciler{
		Client:   hubClient,
		Scheme:   s,
		Recorder: events.NewRecorder(k8sevents.NewFakeRecorder(10)),
	}

	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-orphan"}}

	// Reconcile #1: deletes job1, fails on job2 -> returns error so controller-runtime retries!
	_, err := r.Reconcile(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transient spoke job delete failure")
	// Orphan child Environment has not been deleted yet while hook cleanup is incomplete.
	require.NoError(t, hubClient.Get(ctx, client.ObjectKeyFromObject(orphanEnv), &divergeiov1alpha1.Environment{}))

	// Reconcile #2: transient error clears -> remaining job2 and orphanEnv are both deleted cleanly!
	failSecondJobDelete = false
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.True(t, apierrors.IsNotFound(hubClient.Get(ctx, client.ObjectKeyFromObject(job1), &batchv1.Job{})))
	assert.True(t, apierrors.IsNotFound(hubClient.Get(ctx, client.ObjectKeyFromObject(job2), &batchv1.Job{})))
	assert.True(t, apierrors.IsNotFound(hubClient.Get(ctx, client.ObjectKeyFromObject(orphanEnv), &divergeiov1alpha1.Environment{})))
}
