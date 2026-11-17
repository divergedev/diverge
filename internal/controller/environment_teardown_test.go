package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/finops"
	"github.com/divergedev/diverge/internal/multicluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHandleTeardown_Success(t *testing.T) {
	created := metav1.NewTime(time.Now().Add(-30 * time.Minute))
	now := metav1.Now()
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-env",
			Namespace:         "default",
			CreationTimestamp: created,
			Finalizers:        []string{environmentFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace: "create",
			},
		},
	}
	r, client, dep, rot, db := newTestReconciler(t, env, nil, "")
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-env", Namespace: "default"}}

	res, err := r.handleTeardown(context.Background(), env)
	require.NoError(t, err)
	assert.Empty(t, res)

	assert.True(t, dep.teardownCalled)
	assert.True(t, rot.teardownCalled)
	assert.True(t, db.teardownCalled)

	updatedEnv := &divergeiov1alpha1.Environment{}
	err = client.Get(context.Background(), req.NamespacedName, updatedEnv)
	require.Error(t, err) // Should be deleted

	// Verify historical FinOps ledger recorded the torn-down environment
	records := finops.ListHistoricalRecords(context.Background(), client, nil, "default", time.Now().UTC(), 7, 2*time.Second)
	require.Len(t, records, 1)
	assert.Equal(t, "test-env", records[0].Name)
	assert.Greater(t, records[0].AccruedCostUSD, 0.0)
}

func TestProvisioningAndTeardown_MultiClusterSpokeAndCrossNamespaceRejection(t *testing.T) {
	ctx := context.Background()

	// 1. Cross-namespace kubeconfigSecretRef is rejected with SecretRefValid=False (S5).
	badEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pr-cross-ns",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{Provider: "github", Branch: "feat/x"},
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				ClusterRef: &divergeiov1alpha1.ClusterTargetRef{
					Name: "prod",
					KubeconfigSecretRef: &divergeiov1alpha1.SecretRef{
						Namespace: "kube-system",
						Name:      "admin-kubeconfig",
					},
				},
			},
		},
	}
	rBad, hubBad, _, _, _ := newTestReconciler(t, badEnv, nil, "")
	_, err := rBad.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pr-cross-ns", Namespace: "default"}})
	require.NoError(t, err)

	var gotBad divergeiov1alpha1.Environment
	require.NoError(t, hubBad.Get(ctx, types.NamespacedName{Name: "pr-cross-ns", Namespace: "default"}, &gotBad))
	require.NotEmpty(t, gotBad.Status.Conditions)
	assert.Equal(t, "SecretRefValid", gotBad.Status.Conditions[0].Type)
	assert.Equal(t, metav1.ConditionFalse, gotBad.Status.Conditions[0].Status)
	assert.Equal(t, "CrossNamespaceRef", gotBad.Status.Conditions[0].Reason)

	// 1b. Repairing the reference clears SecretRefValid=False to True.
	gotBad.Spec.Deploy.ClusterRef = nil
	require.NoError(t, hubBad.Update(ctx, &gotBad))
	_, err = rBad.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pr-cross-ns", Namespace: "default"}})
	require.NoError(t, err)
	require.NoError(t, hubBad.Get(ctx, types.NamespacedName{Name: "pr-cross-ns", Namespace: "default"}, &gotBad))
	secCond := meta.FindStatusCondition(gotBad.Status.Conditions, "SecretRefValid")
	require.NotNil(t, secCond)
	assert.Equal(t, metav1.ConditionTrue, secCond.Status)

	// 2. Valid federated environment creates preview namespace + feature ConfigMap on spoke and records TargetCluster.
	spokeEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			UID:        "uid-spoke-1",
			Name:       "pr-spoke",
			Namespace:  "default",
			Finalizers: []string{environmentFinalizer},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{Provider: "github", Branch: "feat/federated"},
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace:       "create",
				ChangedServices: []string{"orders"},
				ClusterRef: &divergeiov1alpha1.ClusterTargetRef{
					Name: "us-west-burst",
					KubeconfigSecretRef: &divergeiov1alpha1.SecretRef{
						Name: "spoke-west-kubeconfig",
					},
				},
			},
			Features: &divergeiov1alpha1.FeatureSpec{
				Provider:  "configmap",
				Overrides: map[string]string{"new_checkout": "true"},
			},
		},
	}
	rSpoke, hubSpoke, _, _, _ := newTestReconciler(t, spokeEnv, nil, "")
	spokeClient := fake.NewClientBuilder().WithScheme(rSpoke.Scheme).Build()
	rSpoke.ClusterClients = &multicluster.StaticResolver{
		Hub: &multicluster.Target{Name: "local", IsRemote: false, Client: hubSpoke},
		Spokes: map[string]*multicluster.Target{
			"us-west-burst": {Name: "us-west-burst", IsRemote: true, Client: spokeClient},
		},
	}

	_, err = rSpoke.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pr-spoke", Namespace: "default"}})
	require.NoError(t, err)

	var gotSpoke divergeiov1alpha1.Environment
	require.NoError(t, hubSpoke.Get(ctx, types.NamespacedName{Name: "pr-spoke", Namespace: "default"}, &gotSpoke))
	assert.Equal(t, "us-west-burst", gotSpoke.Status.TargetCluster)

	var spokePreviewNS corev1.Namespace
	require.NoError(t, spokeClient.Get(ctx, types.NamespacedName{Name: spokeEnv.PreviewNamespace()}, &spokePreviewNS))

	// Feature ConfigMap must exist on spoke (NOT on hub).
	var spokeFeatureCM corev1.ConfigMap
	require.NoError(t, spokeClient.Get(ctx, types.NamespacedName{Name: "diverge-features-pr-spoke", Namespace: spokeEnv.PreviewNamespace()}, &spokeFeatureCM))
	assert.True(t, apierrors.IsNotFound(hubSpoke.Get(ctx, types.NamespacedName{Name: "diverge-features-pr-spoke", Namespace: spokeEnv.PreviewNamespace()}, &corev1.ConfigMap{})))

	// 3. Confirmed remote environment retains finalizer if spoke kubeconfig becomes invalid during teardown.
	delNow := metav1.Now()
	corruptSpokeEnv := gotSpoke.DeepCopy()
	corruptSpokeEnv.DeletionTimestamp = &delNow
	// Use Manager with empty Secret on hub so Resolve returns ValidationError (EmptyKubeconfigSecret).
	emptySec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "spoke-west-kubeconfig", Namespace: "default"},
	}
	require.NoError(t, hubSpoke.Create(ctx, emptySec))
	rSpoke.ClusterClients = multicluster.NewManager(hubSpoke, nil, nil, rSpoke.Scheme)

	res, tdErr := rSpoke.handleTeardown(ctx, corruptSpokeEnv)
	require.Error(t, tdErr, "confirmed remote target must retain finalizer when kubeconfig validation fails")
	assert.Equal(t, 10*time.Second, res.RequeueAfter)
	assert.Contains(t, corruptSpokeEnv.Finalizers, environmentFinalizer)
}

func TestHandleTeardown_PartialFailure(t *testing.T) {
	now := metav1.Now()
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-env",
			Namespace:         "default",
			Finalizers:        []string{environmentFinalizer},
			DeletionTimestamp: &now,
		},
	}
	r, _, dep, rot, db := newTestReconciler(t, env, nil, "")

	dep.teardownErr = errors.New("deployer fails")
	rot.teardownErr = nil
	db.teardownErr = errors.New("db fails")

	res, err := r.handleTeardown(context.Background(), env)
	assert.Equal(t, 10*time.Second, res.RequeueAfter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deployer fails")
	assert.Contains(t, err.Error(), "db fails")

	assert.True(t, dep.teardownCalled)
	assert.True(t, rot.teardownCalled)
	assert.True(t, db.teardownCalled)
}

func TestHandleTeardown_AsyncTeardown(t *testing.T) {
	now := metav1.Now()
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-env",
			Namespace:         "default",
			Finalizers:        []string{environmentFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Routing: divergeiov1alpha1.EnvironmentRouting{
				AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{
					{Protocol: "sqs", Target: "test-queue"},
				},
			},
		},
	}
	r, _, _, _, _ := newTestReconciler(t, env, nil, "")
	fp := &fakeProvisioner{}
	r.AsyncProvisioner = fp

	res, err := r.handleTeardown(context.Background(), env)
	require.NoError(t, err)
	assert.Empty(t, res)

	assert.Equal(t, 1, fp.teardownCalls)
}
