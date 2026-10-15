package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sevents "k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/deployer"
	"github.com/divergedev/diverge/internal/events"
)

func buildDriftTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))
	return scheme
}

func TestBaselineDrift_InitialAndWarnPolicy(t *testing.T) {
	ctx := context.Background()
	scheme := buildDriftTestScheme(t)

	baseCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payments-config",
			Namespace: "default",
		},
		Data: map[string]string{
			"FEATURE_X": "false",
		},
	}

	baselineDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payments-api",
			Namespace: "default",
			Labels: map[string]string{
				"divergedev.com/role":    "baseline",
				"divergedev.com/service": "payments-api",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "payments-api",
							Image: "ghcr.io/org/payments:v1.0.0",
							EnvFrom: []corev1.EnvFromSource{
								{
									ConfigMapRef: &corev1.ConfigMapEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{Name: "payments-config"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-42",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Mode:            "delta",
				ChangedServices: []string{"payments-api"},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&divergeiov1alpha1.Environment{}).
		WithObjects(baseCM, baselineDep, env).
		Build()

	fakeEvents := k8sevents.NewFakeRecorder(20)
	r := &EnvironmentReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: events.NewRecorder(fakeEvents),
	}

	// 1. Initial reconcile records baseline fingerprint
	drifted, synced, err := r.reconcileBaselineDrift(ctx, env)
	require.NoError(t, err)
	assert.False(t, drifted)
	assert.False(t, synced)

	initialFP := env.Annotations[AnnotationBaselineFingerprint]
	assert.Contains(t, initialFP, "sha256:")

	cond := meta.FindStatusCondition(env.Status.Conditions, ConditionBaselineDrift)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "BaselineInSync", cond.Reason)

	// Verify stored Environment has annotation persisted
	storedEnv := &divergeiov1alpha1.Environment{}
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: "pr-42", Namespace: "default"}, storedEnv))
	assert.Equal(t, initialFP, storedEnv.Annotations[AnnotationBaselineFingerprint])

	// 2. Re-evaluating without baseline changes reports no drift
	drifted, synced, err = r.reconcileBaselineDrift(ctx, env)
	require.NoError(t, err)
	assert.False(t, drifted)
	assert.False(t, synced)

	// 3. Mutate referenced ConfigMap data -> triggers warn drift
	baseCM.Data["FEATURE_X"] = "true"
	require.NoError(t, fakeClient.Update(ctx, baseCM))

	drifted, synced, err = r.reconcileBaselineDrift(ctx, env)
	require.NoError(t, err)
	assert.True(t, drifted)
	assert.False(t, synced)

	cond = meta.FindStatusCondition(env.Status.Conditions, ConditionBaselineDrift)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "BaselineUpdated", cond.Reason)

	// Ensure derivePhase keeps Environment in PhaseRunning even when BaselineDriftDetected=True
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:   "ServicesReady",
		Status: metav1.ConditionTrue,
		Reason: "ServicesDeployed",
	})
	assert.Equal(t, divergeiov1alpha1.PhaseRunning, derivePhase(env.Status.Conditions))

	select {
	case evt := <-fakeEvents.Events:
		assert.Contains(t, evt, "BaselineDriftDetected")
	default:
		t.Fatal("expected BaselineDriftDetected warning event")
	}
}

func TestBaselineDrift_SyncPolicyAutoRefreshesPreview(t *testing.T) {
	ctx := context.Background()
	scheme := buildDriftTestScheme(t)

	baselineDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payments-api",
			Namespace: "default",
			Labels: map[string]string{
				"divergedev.com/role":    "baseline",
				"divergedev.com/service": "payments-api",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "payments-api",
							Image: "ghcr.io/org/payments:v1.0.0",
						},
					},
				},
			},
		},
	}

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-77",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationDriftPolicy: DriftPolicySync,
			},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Mode:            "delta",
				ChangedServices: []string{"payments-api"},
			},
			ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{
				ServiceName: "payments-api",
				Port:        8080,
				Image:       "ghcr.io/org/payments:pr-77",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&divergeiov1alpha1.Environment{}).
		WithObjects(baselineDep, env).
		Build()

	directDep := deployer.NewDirectDeployer(fakeClient, &deployer.ServiceConfigFetcher{})
	fakeEvents := k8sevents.NewFakeRecorder(20)
	r := &EnvironmentReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Deployer: directDep,
		Recorder: events.NewRecorder(fakeEvents),
	}

	// Initial deploy + drift snapshot
	statusBase := env.DeepCopy()
	_, done, err := r.reconcileDeploy(ctx, env, statusBase)
	require.NoError(t, err)
	assert.False(t, done)

	firstFP := env.Annotations[AnnotationBaselineFingerprint]
	require.NotEmpty(t, firstFP)

	// Simulate baseline image bump
	baselineDep.Spec.Template.Spec.Containers[0].Image = "ghcr.io/org/payments:v1.1.0"
	require.NoError(t, fakeClient.Update(ctx, baselineDep))

	drifted, synced, err := r.reconcileBaselineDrift(ctx, env)
	require.NoError(t, err)
	assert.False(t, drifted)
	assert.True(t, synced)

	secondFP := env.Annotations[AnnotationBaselineFingerprint]
	assert.NotEqual(t, firstFP, secondFP)

	cond := meta.FindStatusCondition(env.Status.Conditions, ConditionBaselineDrift)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "BaselineSynced", cond.Reason)

	// Verify preview deployment pod template has the updated baseline fingerprint annotation
	var previewDep appsv1.Deployment
	require.NoError(t, fakeClient.Get(ctx, types.NamespacedName{Name: "pr-77-payments-api", Namespace: "default"}, &previewDep))
	assert.Equal(t, secondFP, previewDep.Spec.Template.Annotations[AnnotationBaselineFingerprint])

	select {
	case evt := <-fakeEvents.Events:
		assert.Contains(t, evt, "BaselineSynced")
	default:
		t.Fatal("expected BaselineSynced normal event")
	}
}

func TestBaselineDrift_WatchMappers(t *testing.T) {
	ctx := context.Background()
	scheme := buildDriftTestScheme(t)

	env1 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-1", Namespace: "default"},
	}
	env2 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-2", Namespace: "default"},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(env1, env2).
		Build()

	r := &EnvironmentReconciler{Client: fakeClient, Scheme: scheme}

	// Preview deployment should be ignored
	previewDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-1-api",
			Namespace: "default",
			Labels:    map[string]string{"divergedev.com/role": "preview"},
		},
	}
	assert.Nil(t, r.mapBaselineDeploymentToEnvironments(ctx, previewDep))

	// Baseline deployment should enqueue all active Environments in namespace
	baselineDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api",
			Namespace: "default",
			Labels:    map[string]string{"divergedev.com/role": "baseline"},
		},
	}
	reqs := r.mapBaselineDeploymentToEnvironments(ctx, baselineDep)
	assert.ElementsMatch(t, []ctrl.Request{
		{NamespacedName: types.NamespacedName{Namespace: "default", Name: "pr-1"}},
		{NamespacedName: types.NamespacedName{Namespace: "default", Name: "pr-2"}},
	}, reqs)

	// Managed banner ConfigMap should be ignored
	bannerCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-banner-pr-1",
			Namespace: "default",
			Labels:    map[string]string{"divergedev.com/managed-by": "diverge"},
		},
	}
	assert.Nil(t, r.mapConfigMapToEnvironments(ctx, bannerCM))

	// Shared baseline ConfigMap should enqueue Environments
	sharedCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shared-app-config",
			Namespace: "default",
		},
	}
	cmReqs := r.mapConfigMapToEnvironments(ctx, sharedCM)
	assert.Len(t, cmReqs, 2)
}
