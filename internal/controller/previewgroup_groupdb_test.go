package controller

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8sevents "k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/database/cnpg"
	"github.com/divergedev/diverge/internal/events"
	"github.com/divergedev/diverge/internal/metrics"
	"github.com/divergedev/diverge/internal/multicluster"
	"github.com/divergedev/diverge/internal/previewgroup"
	pkgdb "github.com/divergedev/diverge/pkg/database"
)

type countingGroupDBProvider struct {
	provisions atomic.Int32
	teardowns  atomic.Int32
	lastEnv    string
	dsn        string
}

func (p *countingGroupDBProvider) Provision(_ context.Context, env *divergeiov1alpha1.Environment) (*pkgdb.DatabaseResult, error) {
	p.provisions.Add(1)
	p.lastEnv = env.Name
	return &pkgdb.DatabaseResult{
		DSN:   p.dsn,
		Ready: true,
		EnvVars: map[string]string{
			"DATABASE_URL": p.dsn,
		},
		SetupSQLExecuted: true,
		Message:          "Shared branch ready",
	}, nil
}

func (p *countingGroupDBProvider) Teardown(_ context.Context, env *divergeiov1alpha1.Environment) error {
	p.teardowns.Add(1)
	p.lastEnv = env.Name
	return nil
}

func (p *countingGroupDBProvider) Status(_ context.Context, _ *divergeiov1alpha1.Environment) (*pkgdb.DatabaseStatus, error) {
	return &pkgdb.DatabaseStatus{Provisioned: true}, nil
}

func TestPreviewGroupReconcile_TrueSharedGroupDatabaseAcrossNamespacesAndTTLAnchor(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	anchorName := groupDatabaseAnchorName("feat-checkout")
	cnpgHost := cnpg.ClusterName(anchorName) + "-rw"
	sharedDSN := "postgres://app:secret@" + cnpgHost + ":5432/app"

	createdAt := metav1.NewTime(time.Now().Add(-6 * time.Hour))
	renewedAt := time.Now().UTC().Format(time.RFC3339)

	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "feat-checkout",
			Finalizers: []string{previewGroupFinalizer},
			Labels: map[string]string{
				previewgroup.LabelComposedGroup: "true",
			},
			Annotations: map[string]string{
				previewgroup.AnnotationTTLRenewedAt: renewedAt,
			},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "acme/payments",
				Branch:   "feat/checkout",
			},
			Routing: divergeiov1alpha1.PreviewGroupRouting{
				Mode:        "header",
				HeaderKey:   "x-diverge-env",
				HeaderValue: "feat-checkout",
			},
			Lifecycle: &divergeiov1alpha1.PreviewGroupLifecycle{
				TTL: &metav1.Duration{Duration: 4 * time.Hour},
			},
			Database: &divergeiov1alpha1.EnvironmentDatabase{
				Mode: "cnpg",
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{
					Name:      "payments-api",
					Namespace: "backend",
					Mode:      divergeiov1alpha1.ServiceModeImage,
					Image:     "ghcr.io/acme/payments:pr-1",
				},
				{
					Name:      "orders-api",
					Namespace: "orders",
					Mode:      divergeiov1alpha1.ServiceModeImage,
					Image:     "ghcr.io/acme/orders:pr-2",
				},
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			CreatedAt: &createdAt,
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}).WithObjects(pg).Build()
	dbProv := &countingGroupDBProvider{dsn: sharedDSN}
	r := &PreviewGroupReconciler{
		Client:           c,
		Scheme:           s,
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(20)),
		DatabaseProvider: dbProv,
	}

	// Reconcile: even though CreatedAt is 6h ago and TTL is 4h, renewed-at keeps group alive,
	// and ONE shared DB is provisioned for BOTH services across namespaces!
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "feat-checkout"}})
	require.NoError(t, err)
	assert.Equal(t, int32(1), dbProv.provisions.Load(), "must provision exactly ONE shared group database")
	assert.Equal(t, anchorName, dbProv.lastEnv)

	var currentPG divergeiov1alpha1.PreviewGroup
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "feat-checkout"}, &currentPG))
	assert.True(t, currentPG.DeletionTimestamp.IsZero(), "TTL renewed-at anchor must prevent premature expiry")

	payChildName := childEnvironmentName("feat-checkout", "payments-api")
	ordChildName := childEnvironmentName("feat-checkout", "orders-api")

	var payChild, ordChild divergeiov1alpha1.Environment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: payChildName, Namespace: "backend"}, &payChild))
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: ordChildName, Namespace: "orders"}, &ordChild))
	assert.Equal(t, "true", payChild.Annotations[annotationSharedGroupDB])
	assert.Equal(t, "true", ordChild.Annotations[annotationSharedGroupDB])
	assert.Equal(t, "shared", payChild.Spec.Database.Mode)
	assert.Equal(t, "shared", ordChild.Spec.Database.Mode)

	// Cross-namespace child Secret gets DNS-domain-agnostic Service rewrite (<svc>.backend.svc)
	ordSecretName := sharedGroupDBSecretName(ordChildName)
	var ordSec corev1.Secret
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: ordSecretName, Namespace: "orders"}, &ordSec))
	assert.Equal(t, "true", ordSec.Labels[labelSharedGroupDBSecret])
	assert.Contains(t, string(ordSec.Data["DATABASE_URL"]), "@"+cnpgHost+".backend.svc:5432")

	// Deleting the PreviewGroup tears down children first, then tears down the shared group DB once drained
	require.NoError(t, c.Delete(ctx, &currentPG))
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "feat-checkout"}})
	require.NoError(t, err)
	assert.Equal(t, int32(0), dbProv.teardowns.Load(), "must wait for child Environments to drain before tearing down shared DB")

	// Second teardown reconcile (children deleted in fake client without child finalizers) -> shared DB torn down once!
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "feat-checkout"}})
	require.NoError(t, err)
	assert.Equal(t, int32(1), dbProv.teardowns.Load(), "must tear down shared group DB once after children drain")
}

func TestPreviewGroupReconcile_RealCNPGSharedDBOwnerRefHibernationWakeAndCommentSourceReset(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	standaloneWeb := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-web-101",
			Namespace: "frontend",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "acme/mono",
				MR:       101,
				Branch:   "feat/web",
			},
		},
	}
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "monorepo-group",
			UID:        "pg-uid-1234",
			Finalizers: []string{previewGroupFinalizer},
			Labels: map[string]string{
				previewgroup.LabelComposedGroup: "true",
			},
			Annotations: map[string]string{
				annotationCommentSource:             "github:acme/mono#12",
				previewgroup.AnnotationGroupMembers: `[{"service":"web","namespace":"frontend","provider":"github","project":"acme/mono","mr":101,"environmentName":"pr-web-101","environmentNamespace":"frontend"}]`,
			},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "acme/mono",
				MR:       101, // promoted from #12 -> #101 in same monorepo!
				Branch:   "feat/web",
			},
			Database: &divergeiov1alpha1.EnvironmentDatabase{
				Mode: "cnpg",
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "web", Namespace: "frontend", Mode: divergeiov1alpha1.ServiceModeImage, Image: "acme/web:pr-101"},
				{Name: "api", Namespace: "backend", Mode: divergeiov1alpha1.ServiceModeImage, Image: "acme/api:pr-101"},
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			Phase:     divergeiov1alpha1.PreviewGroupPhasePending,
			CommentID: 99999, // old comment ID from closed PR #12!
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}).WithObjects(standaloneWeb, pg).Build()
	notif := &mockNotifier{nextCommentID: 1001}
	r := &PreviewGroupReconciler{
		Client:   c,
		Scheme:   s,
		Recorder: events.NewRecorder(k8sevents.NewFakeRecorder(20)),
		Notifier: notif,
		DatabaseProvider: &cnpg.CNPGDatabaseProvider{
			Client: c,
			Scheme: s,
		},
	}

	// 1. First reconcile provisions CNPG Cluster (not yet Ready until CNPG operator sets status + secret)
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "monorepo-group"}})
	require.NoError(t, err)

	anchorName := groupDatabaseAnchorName("monorepo-group")
	clusterName := cnpg.ClusterName(anchorName)
	var cluster unstructured.Unstructured
	cluster.SetGroupVersionKind(cnpg.ClusterGVK)
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: "frontend"}, &cluster))
	require.Len(t, cluster.GetOwnerReferences(), 1, "CNPG Cluster must carry PreviewGroup OwnerReference with non-empty UID")
	assert.Equal(t, "PreviewGroup", cluster.GetOwnerReferences()[0].Kind)
	assert.Equal(t, types.UID("pg-uid-1234"), cluster.GetOwnerReferences()[0].UID)

	// Mark CNPG Cluster Ready + create credentials Secret with portless DSN to verify portless URL rewrite
	require.NoError(t, unstructured.SetNestedField(cluster.Object, "Cluster in healthy state", "status", "phase"))
	require.NoError(t, c.Update(ctx, &cluster))
	require.NoError(t, c.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: clusterName + "-app", Namespace: "frontend"},
		Data:       map[string][]byte{"uri": []byte("postgres://app:pw@" + clusterName + "-rw/app")},
	}))

	// 2. Second reconcile sees CNPG Ready, creates child Environments (Pending -> standalone stays awake!),
	//    rewrites portless DSN across namespaces without adding a port, and resets CommentID from closed PR #12!
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "monorepo-group"}})
	require.NoError(t, err)
	assert.NotEmpty(t, notif.createdCalls, "source promotion in monorepo must reset stale CommentID and post fresh comment on PR #101")
	var currentPG divergeiov1alpha1.PreviewGroup
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "monorepo-group"}, &currentPG))
	assert.Equal(t, int64(1001), currentPG.Status.CommentID)
	assert.Equal(t, "github:acme/mono#101", currentPG.Status.CommentSource)

	apiChildName := childEnvironmentName("monorepo-group", "api")
	apiSecretName := sharedGroupDBSecretName(apiChildName)
	var apiSec corev1.Secret
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: apiSecretName, Namespace: "backend"}, &apiSec))
	assert.Equal(t, "postgres://app:pw@"+clusterName+"-rw.frontend.svc/app", string(apiSec.Data["DATABASE_URL"]))

	var currentWeb divergeiov1alpha1.Environment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "pr-web-101", Namespace: "frontend"}, &currentWeb))
	assert.Empty(t, currentWeb.Annotations[metrics.AnnotationHibernated], "standalone preview stays running while child is Pending")

	// Mark web child Running -> next reconcile hibernates standaloneWeb!
	webChildName := childEnvironmentName("monorepo-group", "web")
	var webChild divergeiov1alpha1.Environment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: webChildName, Namespace: "frontend"}, &webChild))
	webChild.Status.Phase = divergeiov1alpha1.PhaseRunning
	require.NoError(t, c.Update(ctx, &webChild))

	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "monorepo-group"}})
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "pr-web-101", Namespace: "frontend"}, &currentWeb))
	assert.Equal(t, "true", currentWeb.Annotations[metrics.AnnotationHibernated])
	assert.Equal(t, "monorepo-group", currentWeb.Annotations[previewgroup.AnnotationAttachedPreviewGroup])

	// 3. Delete PreviewGroup -> after teardown completes, standaloneWeb is automatically woken back up!
	currentPG = divergeiov1alpha1.PreviewGroup{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "monorepo-group"}, &currentPG))
	require.NoError(t, c.Delete(ctx, &currentPG))
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "monorepo-group"}})
	require.NoError(t, err)
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "monorepo-group"}})
	require.NoError(t, err)

	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "pr-web-101", Namespace: "frontend"}, &currentWeb))
	assert.Empty(t, currentWeb.Annotations[metrics.AnnotationHibernated], "teardown of PreviewGroup must wake attached standalone PR Environment")
	assert.Empty(t, currentWeb.Annotations[previewgroup.AnnotationAttachedPreviewGroup])
}

type failingThenOKStatusReporter struct {
	calls     []string
	failState string
}

func (f *failingThenOKStatusReporter) PostCommitStatus(_ context.Context, _ *divergeiov1alpha1.Environment, state, _ string) error {
	f.calls = append(f.calls, state)
	if state == f.failState {
		f.failState = ""
		return errors.New("transient SCM rate limit")
	}
	return nil
}

func TestGroupDB_CrossNamespaceClusterRefNonBlockingMigrationAndOrphanSecretCleanup(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))

	// 1. Cross-namespace unqualified kubeconfigSecretRef with same secret name must NOT be treated as same cluster
	anchorRef := &divergeiov1alpha1.ClusterTargetRef{
		Name:                "spoke-a",
		KubeconfigSecretRef: &divergeiov1alpha1.SecretRef{Name: "kubeconfig"},
	}
	childRef := &divergeiov1alpha1.ClusterTargetRef{
		Name:                "spoke-a",
		KubeconfigSecretRef: &divergeiov1alpha1.SecretRef{Name: "kubeconfig"},
	}
	assert.False(t, divergeiov1alpha1.EqualClusterTargetRefInNamespaces(anchorRef, "ns-anchor", childRef, "ns-child"))
	assert.True(t, divergeiov1alpha1.EqualClusterTargetRefInNamespaces(anchorRef, "ns-anchor", childRef, "ns-anchor"))

	// 2. Non-blocking MigrationJob deploys children immediately, tracks completion ONLY after JobComplete,
	//    and cleans up orphaned diverge-db-env-* Secret on teardown when a child Environment never existed.
	anchorName := groupDatabaseAnchorName("nb-mig-group")
	cnpgHost := cnpg.ClusterName(anchorName) + "-rw"
	blockingFalse := false

	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "nb-mig-group",
			Finalizers: []string{previewGroupFinalizer},
			Labels:     map[string]string{previewgroup.LabelComposedGroup: "true"},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source: divergeiov1alpha1.EnvironmentSource{Provider: "github", Project: "acme/api", Branch: "feat/nb"},
			Database: &divergeiov1alpha1.EnvironmentDatabase{
				Mode: "cnpg",
				MigrationJob: &divergeiov1alpha1.MigrationJobSpec{
					Image:    "acme/migrate:v1",
					Blocking: &blockingFalse,
				},
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "backend", Mode: divergeiov1alpha1.ServiceModeImage, Image: "acme/api:v1"},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}).WithObjects(pg).Build()
	dbProv := &countingGroupDBProvider{dsn: "postgres://app:pw@" + cnpgHost + ":5432/app"}
	r := &PreviewGroupReconciler{
		Client:           c,
		Scheme:           s,
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(20)),
		DatabaseProvider: dbProv,
	}

	// Reconcile 1: creates migration Job (running), allows child creation, does NOT record migration hash yet, requests short requeue
	res1, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "nb-mig-group"}})
	require.NoError(t, err)
	assert.Equal(t, 3*time.Second, res1.RequeueAfter)

	var currentPG divergeiov1alpha1.PreviewGroup
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "nb-mig-group"}, &currentPG))
	assert.Empty(t, currentPG.Annotations[annotationGroupDBMigrationHash], "non-blocking MigrationJob must not mark hash complete while running")

	childName := childEnvironmentName("nb-mig-group", "api")
	var childEnv divergeiov1alpha1.Environment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: childName, Namespace: "backend"}, &childEnv))

	// Simulate child Environment deleted manually while leaving an orphaned diverge-db-env-* Secret
	require.NoError(t, c.Delete(ctx, &childEnv))

	var jobs batchv1.JobList
	require.NoError(t, c.List(ctx, &jobs, client.InNamespace("backend")))
	require.Len(t, jobs.Items, 1)

	// Mark Job complete -> next reconcile persists migration hash!
	jobs.Items[0].Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	require.NoError(t, c.Status().Update(ctx, &jobs.Items[0]))

	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "nb-mig-group"}})
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "nb-mig-group"}, &currentPG))
	assert.NotEmpty(t, currentPG.Annotations[annotationGroupDBMigrationHash])

	// Re-delete child Environment before deleting PreviewGroup so teardown runs with an orphaned child shared Secret
	require.NoError(t, c.Delete(ctx, &childEnv))
	orphanSecretName := sharedGroupDBSecretName(childName)
	var orphanSec corev1.Secret
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: orphanSecretName, Namespace: "backend"}, &orphanSec), "orphaned per-child shared DB Secret must exist before teardown")
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "nb-mig-group"}, &currentPG))
	require.NoError(t, c.Delete(ctx, &currentPG))
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "nb-mig-group"}})
	require.NoError(t, err)

	err = c.Get(ctx, types.NamespacedName{Name: orphanSecretName, Namespace: "backend"}, &orphanSec)
	assert.True(t, apierrors.IsNotFound(err), "orphaned per-child shared DB Secret must be deleted on teardown")
}

func TestController_GenerationGuardFailedReplacementWakeStatusRetryAndAtlasCreate(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))

	// A. Child ObservedGeneration < Generation does NOT hibernate standalone preview;
	//    AND wakeUnclaimedStandaloneEnvironments does NOT transfer attachment to another group whose child is PhaseFailed.
	standalone := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-42",
			Namespace: "default",
			Annotations: map[string]string{
				metrics.AnnotationHibernated:                "true",
				previewgroup.AnnotationAttachedPreviewGroup: "group-old",
				previewgroup.AnnotationGroupHibernated:      "true",
			},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{Provider: "github", Project: "acme/api", MR: 42, Branch: "feat/x"},
		},
	}
	failedGroupChildName := childEnvironmentName("group-failed", "api")
	failedGroupChild := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      failedGroupChildName,
			Namespace: "default",
			Labels:    map[string]string{labelPreviewGroup: "group-failed", labelManagedBy: "diverge-previewgroup"},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{Phase: divergeiov1alpha1.PhaseFailed},
	}
	failedGroup := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "group-failed",
			Finalizers: []string{previewGroupFinalizer},
			Annotations: map[string]string{
				previewgroup.AnnotationGroupMembers: `[{"service":"api","namespace":"default","provider":"github","project":"acme/api","mr":42,"environmentName":"pr-42","environmentNamespace":"default"}]`,
			},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "acme/api:42"},
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			Services: []divergeiov1alpha1.PreviewGroupServiceStatus{{Name: "api", Phase: divergeiov1alpha1.PhaseFailed}},
		},
	}
	cWake := fake.NewClientBuilder().WithScheme(s).WithObjects(standalone, failedGroupChild, failedGroup).Build()
	rWake := &PreviewGroupReconciler{Client: cWake, Scheme: s, Recorder: events.NewRecorder(k8sevents.NewFakeRecorder(10))}
	require.NoError(t, rWake.wakeUnclaimedStandaloneEnvironments(ctx, "group-old", nil))

	var wokeEnv divergeiov1alpha1.Environment
	require.NoError(t, cWake.Get(ctx, types.NamespacedName{Name: "pr-42", Namespace: "default"}, &wokeEnv))
	assert.Empty(t, wokeEnv.Annotations[previewgroup.AnnotationAttachedPreviewGroup], "must not transfer attachment to PhaseFailed replacement")
	assert.Empty(t, wokeEnv.Annotations[metrics.AnnotationHibernated])

	// B. Stale child generation + StatusReporter commit status retry on same SHA after error
	staleStandalone := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-77", Namespace: "default"},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{Provider: "github", Project: "acme/web", MR: 77, Branch: "feat/y"},
		},
	}
	pgGen := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pg-gen-check",
			Finalizers: []string{previewGroupFinalizer},
			Annotations: map[string]string{
				previewgroup.AnnotationGroupMembers: `[{"service":"web","namespace":"default","provider":"github","project":"acme/web","mr":77,"environmentName":"pr-77","environmentNamespace":"default"}]`,
				annotationReportedCommit:            "github:acme/web@sha111:failed",
			},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source: divergeiov1alpha1.EnvironmentSource{Provider: "github", Project: "acme/web", MR: 77, Branch: "feat/y", CommitSHA: "sha111"},
			Routing: divergeiov1alpha1.PreviewGroupRouting{
				Mode: "header", HeaderKey: "x-diverge-env", HeaderValue: "pg-gen-check",
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "web", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "acme/web:77"},
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{Phase: divergeiov1alpha1.PreviewGroupPhaseFailed},
	}
	tempR := &PreviewGroupReconciler{}
	desiredChild := tempR.buildChildEnvironment(pgGen, pgGen.Spec.Services[0], childEnvironmentName("pg-gen-check", "web"), "default")
	desiredChild.Generation = 2
	desiredChild.Status.ObservedGeneration = 1 // older generation!
	desiredChild.Status.Phase = divergeiov1alpha1.PhaseRunning

	reporter := &failingThenOKStatusReporter{failState: "success"}
	cGen := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}).WithObjects(staleStandalone, desiredChild, pgGen).Build()
	rGen := &PreviewGroupReconciler{
		Client:         cGen,
		Scheme:         s,
		Recorder:       events.NewRecorder(k8sevents.NewFakeRecorder(20)),
		StatusReporter: reporter,
	}

	// Reconcile 1: child Generation=2 > ObservedGeneration=1 sets child phase Deploying, keeps standalone running,
	// and does NOT mark group Ready/success commit status yet!
	resGen1, err := rGen.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-gen-check"}})
	require.NoError(t, err)
	assert.Equal(t, 1*time.Second, resGen1.RequeueAfter)
	assert.Empty(t, reporter.calls, "must not post success commit status while child ObservedGeneration < Generation")

	var checkGenPG divergeiov1alpha1.PreviewGroup
	require.NoError(t, cGen.Get(ctx, types.NamespacedName{Name: "pg-gen-check"}, &checkGenPG))
	assert.Equal(t, divergeiov1alpha1.PreviewGroupPhaseDeploying, checkGenPG.Status.Phase)
	require.Len(t, checkGenPG.Status.Services, 1)
	assert.Equal(t, divergeiov1alpha1.PhaseDeploying, checkGenPG.Status.Services[0].Phase)

	var checkStandalone divergeiov1alpha1.Environment
	require.NoError(t, cGen.Get(ctx, types.NamespacedName{Name: "pr-77", Namespace: "default"}, &checkStandalone))
	assert.Empty(t, checkStandalone.Annotations[metrics.AnnotationHibernated], "must not hibernate standalone env on stale child ObservedGeneration")

	// Bring ObservedGeneration up to date: Reconcile 2 hibernates standalone env AND attempts "success" commit status (fails -> requeues),
	// then Reconcile 3 succeeds cleanly without repeating after success.
	require.NoError(t, cGen.Get(ctx, types.NamespacedName{Name: desiredChild.Name, Namespace: "default"}, desiredChild))
	desiredChild.Status.ObservedGeneration = desiredChild.Generation
	require.NoError(t, cGen.Update(ctx, desiredChild))

	resGen2, err := rGen.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-gen-check"}})
	require.NoError(t, err)
	assert.Equal(t, 1*time.Second, resGen2.RequeueAfter)
	assert.Equal(t, []string{"success"}, reporter.calls)
	require.NoError(t, cGen.Get(ctx, types.NamespacedName{Name: "pr-77", Namespace: "default"}, &checkStandalone))
	assert.Equal(t, "true", checkStandalone.Annotations[metrics.AnnotationHibernated])

	_, err = rGen.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-gen-check"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"success", "success"}, reporter.calls)

	// C. ensureAtlasCR preserves DSN Secret on ambiguous timeout/NotFound re-read, and deletes only on definitive rejection
	atlasEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pgdb-atlas", Namespace: "default"},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Mode:  "cnpg",
				Atlas: &divergeiov1alpha1.AtlasSpec{Mode: "versioned", MigrationConfigMap: "mig-cm"},
			},
		},
	}
	baseAtlasClient := fake.NewClientBuilder().WithScheme(s).Build()
	createErrToReturn := errors.New("context deadline exceeded after write")
	interceptedAtlas := interceptor.NewClient(baseAtlasClient, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == "AtlasMigration" {
				return apierrors.NewNotFound(schema.GroupResource{Group: "db.atlasgo.io", Resource: "atlasmigrations"}, key.Name)
			}
			return c.Get(ctx, key, obj, opts...)
		},
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == "AtlasMigration" {
				return createErrToReturn
			}
			return c.Create(ctx, obj, opts...)
		},
	})
	envRec := &EnvironmentReconciler{Client: interceptedAtlas, Scheme: s}
	err = envRec.ensureAtlasCR(ctx, atlasEnv, &pkgdb.DatabaseResult{DSN: "postgres://u:p@host:5432/db"})
	require.Error(t, err)
	var dsnSecs corev1.SecretList
	require.NoError(t, baseAtlasClient.List(ctx, &dsnSecs, client.InNamespace("default")))
	assert.Len(t, dsnSecs.Items, 1, "DSN Secret must not be deleted on ambiguous timeout even when re-read returns NotFound")

	createErrToReturn = apierrors.NewInvalid(schema.GroupKind{Group: "db.atlasgo.io", Kind: "AtlasMigration"}, "bad", nil)
	err = envRec.ensureAtlasCR(ctx, atlasEnv, &pkgdb.DatabaseResult{DSN: "postgres://u:p@host:5432/db"})
	require.Error(t, err)
	require.NoError(t, baseAtlasClient.List(ctx, &dsnSecs, client.InNamespace("default")))
	assert.Empty(t, dsnSecs.Items, "DSN Secret must be deleted after definitive invalid rejection")
}

func TestEnvironmentProvisioner_SharedDBMissingSecretClearsRunningPhase(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	child := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "pg-grp-api-1234",
			Namespace:   "default",
			Annotations: map[string]string{annotationSharedGroupDB: "true"},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source:   divergeiov1alpha1.EnvironmentSource{Provider: "github", Project: "acme/api", Branch: "feat/x"},
			Database: divergeiov1alpha1.EnvironmentDatabase{Mode: "shared", ConnectionRef: "missing-secret"},
			Deploy:   divergeiov1alpha1.EnvironmentDeploy{Mode: "delta", Namespace: "same"},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{Phase: divergeiov1alpha1.PhaseRunning},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.Environment{}).WithObjects(child).Build()
	r := &EnvironmentReconciler{
		Client:   c,
		Scheme:   s,
		Recorder: events.NewRecorder(k8sevents.NewFakeRecorder(10)),
	}
	_, _, done, err := r.reconcileProvisioningWithTarget(ctx, child, child.DeepCopy())
	require.NoError(t, err)
	assert.True(t, done)

	var updated divergeiov1alpha1.Environment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: child.Name, Namespace: "default"}, &updated))
	assert.Equal(t, divergeiov1alpha1.PhaseDeploying, updated.Status.Phase, "stale Running phase must clear when shared DB Secret is missing")
}

type failingProvDB struct {
	err error
}

func (f *failingProvDB) Provision(_ context.Context, _ *divergeiov1alpha1.Environment) (*pkgdb.DatabaseResult, error) {
	return nil, f.err
}
func (f *failingProvDB) Teardown(_ context.Context, _ *divergeiov1alpha1.Environment) error {
	return nil
}
func (f *failingProvDB) Status(_ context.Context, _ *divergeiov1alpha1.Environment) (*pkgdb.DatabaseStatus, error) {
	return &pkgdb.DatabaseStatus{}, nil
}

type nsAwareSpokeResolver struct {
	byNamespace map[string]*multicluster.Target
	fallback    *multicluster.Target
}

func (r *nsAwareSpokeResolver) Resolve(_ context.Context, env *divergeiov1alpha1.Environment) (*multicluster.Target, error) {
	if t, ok := r.byNamespace[env.Namespace]; ok {
		return t, nil
	}
	return r.fallback, nil
}

func TestPR351_ControllerGroupDBFailurePromotionNonBlockingFailSwitchOffSpokeAndStaleWake(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))

	// 1. GroupDB failure branch after source promotion clears stale CommentID before PostGroupFailed and pairs new ID + CommentSource
	pgFailPromo := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pg-fail-promo",
			Labels:     map[string]string{previewgroup.LabelComposedGroup: "true"},
			Finalizers: []string{previewGroupFinalizer},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source:   divergeiov1alpha1.EnvironmentSource{Provider: "github", Project: "acme/mono", MR: 101, Branch: "feat/101"},
			Database: &divergeiov1alpha1.EnvironmentDatabase{Mode: "cnpg"},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "img:101"},
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			Phase:         divergeiov1alpha1.PreviewGroupPhaseFailed, // same phase as failure -> only sourcePromoted triggers notification
			CommentID:     888,
			CommentSource: "github:acme/mono#10",
		},
	}
	cFail := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}).WithObjects(pgFailPromo).Build()
	notifFail := &mockNotifier{nextCommentID: 1001}
	rFail := &PreviewGroupReconciler{
		Client:           cFail,
		Scheme:           s,
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(10)),
		Notifier:         notifFail,
		DatabaseProvider: &failingProvDB{err: errors.New("cnpg quota exceeded")},
	}
	_, err := rFail.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-fail-promo"}})
	require.Error(t, err)
	require.Len(t, notifFail.failedCalls, 1)
	assert.Equal(t, int64(1001), notifFail.failedCalls[0].pg.Status.CommentID, "CommentID must reset before PostGroupFailed")
	var gotFailPG divergeiov1alpha1.PreviewGroup
	require.NoError(t, cFail.Get(ctx, types.NamespacedName{Name: "pg-fail-promo"}, &gotFailPG))
	assert.Equal(t, int64(1001), gotFailPG.Status.CommentID)
	assert.Equal(t, "github:acme/mono#101", gotFailPG.Status.CommentSource)

	// 2. Non-blocking MigrationJob terminal failure keeps shared DB ready without blocking child deployment
	falseVal := false
	pgNBFail := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pg-nb-fail",
			Labels:     map[string]string{previewgroup.LabelComposedGroup: "true"},
			Finalizers: []string{previewGroupFinalizer},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Database: &divergeiov1alpha1.EnvironmentDatabase{
				Mode: "cnpg",
				MigrationJob: &divergeiov1alpha1.MigrationJobSpec{
					Image:    "migrate:latest",
					Args:     []string{"up"},
					Blocking: &falseVal,
				},
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "img:1"},
			},
		},
	}
	anchorName := groupDatabaseAnchorName("pg-nb-fail")
	nbProv := &countingGroupDBProvider{dsn: "postgres://u:p@db-rw.default.svc/app"}
	cNBFail := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}).WithObjects(pgNBFail).Build()
	rNBFail := &PreviewGroupReconciler{
		Client:           cNBFail,
		Scheme:           s,
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(10)),
		DatabaseProvider: nbProv,
	}
	_, err = rNBFail.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-nb-fail"}})
	require.NoError(t, err)

	var nbJobs batchv1.JobList
	require.NoError(t, cNBFail.List(ctx, &nbJobs, client.InNamespace("default")))
	require.Len(t, nbJobs.Items, 1)
	nbJobs.Items[0].Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"}}
	require.NoError(t, cNBFail.Status().Update(ctx, &nbJobs.Items[0]))

	require.NoError(t, cNBFail.Get(ctx, types.NamespacedName{Name: "pg-nb-fail"}, pgNBFail))
	outVars, _, handled, ready, reqNeeded, gErr := rNBFail.reconcileGroupDatabase(ctx, pgNBFail)
	require.NoError(t, gErr, "terminal failure of explicitly non-blocking group migration must not fail reconcileGroupDatabase")
	assert.True(t, handled)
	assert.True(t, ready)
	assert.False(t, reqNeeded)
	assert.NotEmpty(t, outVars["DATABASE_URL"])
	assert.Contains(t, anchorName, "pg-nb-fail")

	// 3. Switch-off shared DB preserves diverge-group-db-env-* Secret until child workload ObservedGeneration catches up,
	//    AND spoke cleanup resolves inherited clusterRef in child's svcNS (not anchorNS).
	switchChildName := childEnvironmentName("pg-switch", "api")
	switchSecName := sharedGroupDBSecretName(switchChildName)
	spokeSwitchRef := &divergeiov1alpha1.ClusterTargetRef{
		Name:                "spoke-a",
		KubeconfigSecretRef: &divergeiov1alpha1.SecretRef{Name: "kubeconfig"},
	}
	switchChild := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        switchChildName,
			Namespace:   "backend",
			Generation:  3,
			Labels:      map[string]string{labelPreviewGroup: "pg-switch", labelManagedBy: "diverge-previewgroup"},
			Annotations: map[string]string{annotationSharedGroupDBDraining: "true"},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy:   divergeiov1alpha1.EnvironmentDeploy{Namespace: "same", ClusterRef: spokeSwitchRef},
			Database: divergeiov1alpha1.EnvironmentDatabase{Mode: "fresh"},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{ObservedGeneration: 2}, // workload hasn't observed switch-off yet
	}
	pgSwitch := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pg-switch",
			Finalizers: []string{previewGroupFinalizer},
			Annotations: map[string]string{
				annotationGroupDBNamespace: "default",
				annotationGroupDBProvider:  "cnpg",
			},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			ClusterRef: spokeSwitchRef,
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "backend", Mode: divergeiov1alpha1.ServiceModeImage, Image: "img:1"},
			},
		},
	}
	spokeAnchorClient := fake.NewClientBuilder().WithScheme(s).Build()
	spokeBackendClient := fake.NewClientBuilder().WithScheme(s).WithObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      switchSecName,
			Namespace: "backend",
			Labels: map[string]string{
				labelPreviewGroup:           "pg-switch",
				"divergedev.com/managed-by": "diverge",
				labelSharedGroupDBSecret:    "true",
			},
		},
	}).Build()
	hubSwitch := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}).WithObjects(pgSwitch, switchChild).Build()
	rSwitch := &PreviewGroupReconciler{
		Client:           hubSwitch,
		Scheme:           s,
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(10)),
		DatabaseProvider: &countingGroupDBProvider{dsn: "postgres://u:p@db-rw.default.svc/app"},
		ClusterClients: &nsAwareSpokeResolver{
			byNamespace: map[string]*multicluster.Target{
				"backend": {Name: "spoke-a", IsRemote: true, Client: spokeBackendClient},
			},
			fallback: &multicluster.Target{Name: "spoke-a", IsRemote: true, Client: spokeAnchorClient},
		},
	}

	// Reconcile while ObservedGeneration < Generation: Secret kept on spokeBackendClient and group DB annotations stay pinned
	_, err = rSwitch.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-switch"}})
	require.NoError(t, err)
	var keptSpokeSec corev1.Secret
	require.NoError(t, spokeBackendClient.Get(ctx, types.NamespacedName{Name: switchSecName, Namespace: "backend"}, &keptSpokeSec))

	// Even when ObservedGeneration == Generation, failed deploy or stale ServicesReady must keep spoke Secret
	require.NoError(t, hubSwitch.Get(ctx, types.NamespacedName{Name: switchChildName, Namespace: "backend"}, switchChild))
	switchChild.Status.ObservedGeneration = switchChild.Generation
	switchChild.Status.Phase = divergeiov1alpha1.PhaseFailed
	switchChild.Status.Conditions = []metav1.Condition{{
		Type:               "ServicesReady",
		Status:             metav1.ConditionFalse,
		ObservedGeneration: switchChild.Generation,
		Reason:             "DeployFailed",
		Message:            "boom",
	}}
	require.NoError(t, hubSwitch.Update(ctx, switchChild))

	_, err = rSwitch.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-switch"}})
	require.NoError(t, err)
	err = spokeBackendClient.Get(ctx, types.NamespacedName{Name: switchSecName, Namespace: "backend"}, &keptSpokeSec)
	require.NoError(t, err, "shared DB Secret must be preserved while replacement deploy is failing at current generation")

	// PhaseRunning alone with stale ServicesReady.ObservedGeneration (< Generation) must still preserve Secret
	require.NoError(t, hubSwitch.Get(ctx, types.NamespacedName{Name: switchChildName, Namespace: "backend"}, switchChild))
	switchChild.Status.Phase = divergeiov1alpha1.PhaseRunning
	switchChild.Status.Conditions = []metav1.Condition{{
		Type:               "ServicesReady",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: switchChild.Generation - 1,
		Reason:             "ServicesDeployed",
		Message:            "stale generation",
	}}
	require.NoError(t, hubSwitch.Update(ctx, switchChild))

	_, err = rSwitch.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-switch"}})
	require.NoError(t, err)
	err = spokeBackendClient.Get(ctx, types.NamespacedName{Name: switchSecName, Namespace: "backend"}, &keptSpokeSec)
	require.NoError(t, err, "shared DB Secret must be preserved while ServicesReady.ObservedGeneration lags child.Generation")

	// Workload deploys at current Generation (ObservedGeneration == Generation, PhaseRunning, ServicesReady=True@Gen) -> deletes spoke Secret
	require.NoError(t, hubSwitch.Get(ctx, types.NamespacedName{Name: switchChildName, Namespace: "backend"}, switchChild))
	switchChild.Status.ObservedGeneration = switchChild.Generation
	switchChild.Status.Phase = divergeiov1alpha1.PhaseRunning
	switchChild.Status.Conditions = []metav1.Condition{{
		Type:               "ServicesReady",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: switchChild.Generation,
		Reason:             "ServicesDeployed",
		Message:            "ok",
	}}
	require.NoError(t, hubSwitch.Update(ctx, switchChild))

	_, err = rSwitch.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-switch"}})
	require.NoError(t, err)
	err = spokeBackendClient.Get(ctx, types.NamespacedName{Name: switchSecName, Namespace: "backend"}, &keptSpokeSec)
	assert.True(t, apierrors.IsNotFound(err), "shared DB Secret on svcNS spoke must be deleted after child workload deploys switch-off")
	var switchedPG divergeiov1alpha1.PreviewGroup
	require.NoError(t, hubSwitch.Get(ctx, types.NamespacedName{Name: "pg-switch"}, &switchedPG))
	assert.Empty(t, switchedPG.Annotations[annotationGroupDBNamespace])
	assert.Empty(t, switchedPG.Annotations[annotationGroupDBProvider])

	// 4. Stale replacement PreviewGroup (Ready.ObservedGeneration < Generation) wakes standalone preview instead of transferring attachment
	standaloneStale := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-88",
			Namespace: "default",
			Annotations: map[string]string{
				metrics.AnnotationHibernated:                "true",
				previewgroup.AnnotationAttachedPreviewGroup: "group-old",
				previewgroup.AnnotationGroupHibernated:      "true",
			},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{Provider: "github", Project: "acme/api", MR: 88, Branch: "feat/88"},
		},
	}
	staleRepChild := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       childEnvironmentName("group-stale-rep", "api"),
			Namespace:  "default",
			Generation: 2,
			Labels:     map[string]string{labelPreviewGroup: "group-stale-rep", labelManagedBy: "diverge-previewgroup"},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{ServiceName: "api", Image: "acme/api:new"},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			ObservedGeneration: 2,
			Phase:              divergeiov1alpha1.PhaseRunning,
		},
	}
	staleRepGroup := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "group-stale-rep",
			Generation: 3, // newer group generation not yet observed by Ready condition
			Annotations: map[string]string{
				previewgroup.AnnotationGroupMembers: `[{"service":"api","namespace":"default","provider":"github","project":"acme/api","mr":88,"environmentName":"pr-88","environmentNamespace":"default"}]`,
			},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "acme/api:new"},
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			Services: []divergeiov1alpha1.PreviewGroupServiceStatus{{Name: "api", Phase: divergeiov1alpha1.PhaseRunning}},
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 2},
			},
		},
	}
	cStaleWake := fake.NewClientBuilder().WithScheme(s).WithObjects(standaloneStale, staleRepChild, staleRepGroup).Build()
	rStaleWake := &PreviewGroupReconciler{Client: cStaleWake, Scheme: s, Recorder: events.NewRecorder(k8sevents.NewFakeRecorder(10))}
	require.NoError(t, rStaleWake.wakeUnclaimedStandaloneEnvironments(ctx, "group-old", nil))

	var wokenFromStale divergeiov1alpha1.Environment
	require.NoError(t, cStaleWake.Get(ctx, types.NamespacedName{Name: "pr-88", Namespace: "default"}, &wokenFromStale))
	assert.Empty(t, wokenFromStale.Annotations[previewgroup.AnnotationAttachedPreviewGroup])
	assert.Empty(t, wokenFromStale.Annotations[metrics.AnnotationHibernated])
}

func TestEnvironmentReconciler_FailedDeployDoesNotAdvanceObservedGeneration(t *testing.T) {
	ctx := context.Background()
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "deploy-guard-env",
			Namespace:   "default",
			Generation:  2,
			Finalizers:  []string{environmentFinalizer},
			Annotations: map[string]string{annotationSharedGroupDBDraining: "true"},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace: "create",
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			ObservedGeneration: 1,
			Phase:              divergeiov1alpha1.PhaseRunning,
			Conditions: []metav1.Condition{{
				Type:               "ServicesReady",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 1,
				Reason:             "ServicesDeployed",
				Message:            "previous gen ready",
			}},
		},
	}

	r, c, dep, _, _ := newTestReconciler(t, env, &pkgdb.DatabaseResult{Ready: true}, "https://guard.example.com")
	dep.deployErr = errors.New("image pull failed")

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: env.Name, Namespace: env.Namespace}}
	_, err := r.Reconcile(ctx, req)
	require.Error(t, err)

	var updated divergeiov1alpha1.Environment
	require.NoError(t, c.Get(ctx, req.NamespacedName, &updated))
	assert.Equal(t, int64(1), updated.Status.ObservedGeneration, "failed deploy must not advance ObservedGeneration")
	assert.Equal(t, divergeiov1alpha1.PhaseFailed, updated.Status.Phase)
	svcReady := meta.FindStatusCondition(updated.Status.Conditions, "ServicesReady")
	require.NotNil(t, svcReady)
	assert.Equal(t, metav1.ConditionFalse, svcReady.Status)
	assert.Equal(t, int64(2), svcReady.ObservedGeneration)
	assert.True(t, childStillUsesSharedDBSecret(&updated, sharedGroupDBSecretName(env.Name)))

	dep.deployErr = nil
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)

	require.NoError(t, c.Get(ctx, req.NamespacedName, &updated))
	assert.Equal(t, int64(2), updated.Status.ObservedGeneration, "successful deploy must advance ObservedGeneration")
	assert.Equal(t, divergeiov1alpha1.PhaseRunning, updated.Status.Phase)
	svcReady = meta.FindStatusCondition(updated.Status.Conditions, "ServicesReady")
	require.NotNil(t, svcReady)
	assert.Equal(t, metav1.ConditionTrue, svcReady.Status)
	assert.Equal(t, int64(2), svcReady.ObservedGeneration)
	assert.False(t, childStillUsesSharedDBSecret(&updated, sharedGroupDBSecretName(env.Name)))
}

func TestSharedDBTeardownWaitsForRolloutAndOldPods(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, appsv1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))

	childName := childEnvironmentName("pg-rollout-guard", "api")
	secName := sharedGroupDBSecretName(childName)
	spokeRef := &divergeiov1alpha1.ClusterTargetRef{
		Name:                "spoke-a",
		KubeconfigSecretRef: &divergeiov1alpha1.SecretRef{Name: "kubeconfig"},
	}

	child := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        childName,
			Namespace:   "backend",
			Generation:  3,
			Labels:      map[string]string{labelPreviewGroup: "pg-rollout-guard", labelManagedBy: "diverge-previewgroup"},
			Annotations: map[string]string{annotationSharedGroupDBDraining: "true"},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy: divergeiov1alpha1.EnvironmentDeploy{
				Namespace:  "same",
				ClusterRef: spokeRef,
			},
			Database: divergeiov1alpha1.EnvironmentDatabase{Mode: "fresh"},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			ObservedGeneration: 3,
			Phase:              divergeiov1alpha1.PhaseRunning,
			Conditions: []metav1.Condition{{
				Type:               "ServicesReady",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 3,
				Reason:             "ServicesDeployed",
				Message:            "applied",
			}},
		},
	}
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pg-rollout-guard",
			Finalizers: []string{previewGroupFinalizer},
			Annotations: map[string]string{
				annotationGroupDBNamespace: "default",
			},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			ClusterRef: spokeRef,
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "backend", Mode: divergeiov1alpha1.ServiceModeImage, Image: "img:2"},
			},
		},
	}

	rollout := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Rollout",
			"metadata": map[string]interface{}{
				"name":       childName,
				"namespace":  "backend",
				"generation": int64(2),
				"labels": map[string]interface{}{
					"divergedev.com/environment": childName,
				},
			},
			"spec": map[string]interface{}{
				"replicas": int64(2),
			},
			"status": map[string]interface{}{
				"observedGeneration": "2",
				"phase":              "Progressing",
				"stableRS":           "old-rs",
				"currentPodHash":     "new-hash",
				"replicas":           int64(2),
				"updatedReplicas":    int64(1),
			},
		},
	}

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secName,
			Namespace: "backend",
			Labels: map[string]string{
				labelPreviewGroup:           "pg-rollout-guard",
				"divergedev.com/managed-by": "diverge",
				labelSharedGroupDBSecret:    "true",
			},
		},
	}

	spokeBackendClient := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&corev1.Pod{}, &appsv1.Deployment{}).WithObjects(sec, rollout).Build()
	spokeAnchorClient := fake.NewClientBuilder().WithScheme(s).Build()
	hubClient := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}, &divergeiov1alpha1.Environment{}).WithObjects(pg, child).Build()
	dbProv := &countingGroupDBProvider{dsn: "postgres://u:p@db-rw.default.svc/app"}
	r := &PreviewGroupReconciler{
		Client:           hubClient,
		Scheme:           s,
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(10)),
		DatabaseProvider: dbProv,
		ClusterClients: &nsAwareSpokeResolver{
			byNamespace: map[string]*multicluster.Target{
				"backend": {Name: "spoke-a", IsRemote: true, Client: spokeBackendClient},
			},
			fallback: &multicluster.Target{Name: "spoke-a", IsRemote: true, Client: spokeAnchorClient},
		},
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-rollout-guard"}}

	// 1. Even with ServicesReady=True and ObservedGeneration==Generation, an in-progress Rollout delays teardown & preserves Secret
	res, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Greater(t, res.RequeueAfter, time.Duration(0))
	assert.Zero(t, dbProv.teardowns.Load(), "must not tear down shared DB while Argo Rollout is still progressing")
	var keptSec corev1.Secret
	require.NoError(t, spokeBackendClient.Get(ctx, types.NamespacedName{Name: secName, Namespace: "backend"}, &keptSec))

	// 2. Even after Rollout becomes Healthy, a non-terminal old Pod referencing the Secret delays teardown
	require.NoError(t, spokeBackendClient.Get(ctx, types.NamespacedName{Name: childName, Namespace: "backend"}, rollout))
	require.NoError(t, unstructured.SetNestedField(rollout.Object, "Healthy", "status", "phase"))
	require.NoError(t, unstructured.SetNestedField(rollout.Object, "new-hash", "status", "stableRS"))
	require.NoError(t, unstructured.SetNestedField(rollout.Object, int64(2), "status", "updatedReplicas"))
	require.NoError(t, spokeBackendClient.Update(ctx, rollout))

	oldPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childName + "-old-pod",
			Namespace: "backend",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "api",
				Image: "img:1",
				EnvFrom: []corev1.EnvFromSource{{
					SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: secName}},
				}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	require.NoError(t, spokeBackendClient.Create(ctx, oldPod))

	res, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Greater(t, res.RequeueAfter, time.Duration(0))
	assert.Zero(t, dbProv.teardowns.Load(), "must not tear down shared DB while an old Running Pod still mounts its Secret")
	require.NoError(t, spokeBackendClient.Get(ctx, types.NamespacedName{Name: secName, Namespace: "backend"}, &keptSec))

	// 3. Once the old Pod terminates (PodSucceeded) and Rollout is Healthy, teardown deletes the Secret and clears pinned annotations
	require.NoError(t, spokeBackendClient.Get(ctx, types.NamespacedName{Name: oldPod.Name, Namespace: "backend"}, oldPod))
	oldPod.Status.Phase = corev1.PodSucceeded
	require.NoError(t, spokeBackendClient.Status().Update(ctx, oldPod))

	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, int32(1), dbProv.teardowns.Load())
	err = spokeBackendClient.Get(ctx, types.NamespacedName{Name: secName, Namespace: "backend"}, &keptSec)
	assert.True(t, apierrors.IsNotFound(err))
	var updatedPG divergeiov1alpha1.PreviewGroup
	require.NoError(t, hubClient.Get(ctx, types.NamespacedName{Name: "pg-rollout-guard"}, &updatedPG))
	assert.Empty(t, updatedPG.Annotations[annotationGroupDBNamespace])
	assert.Empty(t, updatedPG.Annotations[annotationGroupDBProvider])

	// 4. Per-service opt-out while group DB stays active: rolling Deployment / live Pod protects group Secret and signals requeue,
	//    while preserving both the surviving group Secret (web) and api's own per-service Secret.
	pgActive := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "pg-active-optout",
			Labels:      map[string]string{previewgroup.LabelComposedGroup: "true"},
			Annotations: map[string]string{annotationGroupDBNamespace: "default", annotationGroupDBProvider: "cnpg"},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			ClusterRef: spokeRef,
			Database:   &divergeiov1alpha1.EnvironmentDatabase{Mode: "fresh"},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "backend", Mode: divergeiov1alpha1.ServiceModeImage, Image: "img:2", Database: &divergeiov1alpha1.EnvironmentDatabase{Mode: "fresh"}},
				{Name: "web", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "web:1"},
			},
		},
	}
	optOutName := childEnvironmentName("pg-active-optout", "api")
	optOutSecName := sharedGroupDBSecretName(optOutName)
	apiPerSvcSecName := legacySharedGroupDBSecretName(optOutName)
	webChildName := childEnvironmentName("pg-active-optout", "web")
	webSecName := sharedGroupDBSecretName(webChildName)
	optOutChild := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        optOutName,
			Namespace:   "backend",
			Generation:  2,
			UID:         "uid-optout-child",
			Labels:      map[string]string{labelPreviewGroup: "pg-active-optout", labelManagedBy: "diverge-previewgroup"},
			Annotations: map[string]string{annotationSharedGroupDBDraining: "true"},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy:   divergeiov1alpha1.EnvironmentDeploy{Namespace: "same", ClusterRef: spokeRef},
			Database: divergeiov1alpha1.EnvironmentDatabase{Mode: "fresh"},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			ObservedGeneration: 2,
			Phase:              divergeiov1alpha1.PhaseRunning,
			Conditions: []metav1.Condition{{
				Type:               "ServicesReady",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 2,
				Reason:             "ServicesDeployed",
			}},
		},
	}
	optOutSec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      optOutSecName,
			Namespace: "backend",
			Labels:    map[string]string{labelPreviewGroup: "pg-active-optout", "divergedev.com/managed-by": "diverge", labelSharedGroupDBSecret: "true"},
		},
	}
	isCtrlTrue := true
	apiPerSvcSec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      apiPerSvcSecName,
			Namespace: "backend",
			Labels:    map[string]string{"divergedev.com/environment": optOutName, "divergedev.com/managed-by": "diverge"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "divergedev.com/v1alpha1",
				Kind:       "Environment",
				Name:       optOutName,
				UID:        optOutChild.UID,
				Controller: &isCtrlTrue,
			}},
		},
	}
	webGroupSec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webSecName,
			Namespace: "default",
			Labels:    map[string]string{labelPreviewGroup: "pg-active-optout", "divergedev.com/managed-by": "diverge", labelSharedGroupDBSecret: "true"},
		},
	}
	require.NoError(t, spokeAnchorClient.Create(ctx, webGroupSec))
	rollingReplicas := int32(2)
	rollingDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       optOutName,
			Namespace:  "backend",
			Generation: 2,
			Labels:     map[string]string{"divergedev.com/environment": optOutName},
		},
		Spec: appsv1.DeploymentSpec{Replicas: &rollingReplicas},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 2,
			Replicas:           2,
			UpdatedReplicas:    1,
		},
	}
	spokeOptOutClient := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&corev1.Pod{}, &appsv1.Deployment{}).WithObjects(optOutSec, apiPerSvcSec, rollingDep).Build()
	hubOptOut := fake.NewClientBuilder().WithScheme(s).WithObjects(pgActive, optOutChild).Build()
	rOptOut := &PreviewGroupReconciler{
		Client: hubOptOut,
		Scheme: s,
		ClusterClients: &nsAwareSpokeResolver{
			byNamespace: map[string]*multicluster.Target{
				"backend": {Name: "spoke-a", IsRemote: true, Client: spokeOptOutClient},
			},
			fallback: &multicluster.Target{Name: "spoke-a", IsRemote: true, Client: spokeAnchorClient},
		},
	}
	desired := map[string]bool{
		"backend/" + optOutName:   true,
		"default/" + webChildName: true,
	}

	requeue, cErr := rOptOut.cleanupOrphanedChildSharedDBSecrets(ctx, pgActive, desired)
	require.NoError(t, cErr)
	assert.True(t, requeue, "must request requeue while opted-out child Deployment still has un-updated replicas")
	require.NoError(t, spokeOptOutClient.Get(ctx, types.NamespacedName{Name: optOutSecName, Namespace: "backend"}, &keptSec))
	require.NoError(t, spokeAnchorClient.Get(ctx, types.NamespacedName{Name: webSecName, Namespace: "default"}, &keptSec))
	require.NoError(t, spokeOptOutClient.Get(ctx, types.NamespacedName{Name: apiPerSvcSecName, Namespace: "backend"}, &keptSec))

	require.NoError(t, spokeOptOutClient.Get(ctx, types.NamespacedName{Name: optOutName, Namespace: "backend"}, rollingDep))
	rollingDep.Status.UpdatedReplicas = 2
	require.NoError(t, spokeOptOutClient.Status().Update(ctx, rollingDep))

	requeue, cErr = rOptOut.cleanupOrphanedChildSharedDBSecrets(ctx, pgActive, desired)
	require.NoError(t, cErr)
	assert.False(t, requeue)
	err = spokeOptOutClient.Get(ctx, types.NamespacedName{Name: optOutSecName, Namespace: "backend"}, &keptSec)
	assert.True(t, apierrors.IsNotFound(err), "opted-out api group Secret must be deleted once rollout drains")
	require.NoError(t, spokeAnchorClient.Get(ctx, types.NamespacedName{Name: webSecName, Namespace: "default"}, &keptSec), "surviving web group Secret must be preserved")
	require.NoError(t, spokeOptOutClient.Get(ctx, types.NamespacedName{Name: apiPerSvcSecName, Namespace: "backend"}, &keptSec), "opted-out api per-service Secret must be preserved")
}

func TestSharedDB_OptOutConnectionRefAndTakeover(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, appsv1.AddToScheme(s))

	childName := childEnvironmentName("pg-takeover", "api")
	legacySecName := legacySharedGroupDBSecretName(childName)
	groupSecName := sharedGroupDBSecretName(childName)

	child := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       childName,
			Namespace:  "default",
			UID:        "uid-takeover-api",
			Generation: 2,
			Labels:     map[string]string{labelPreviewGroup: "pg-takeover", labelManagedBy: "diverge-previewgroup"},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{
				Mode:          "fresh",
				ConnectionRef: legacySecName,
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			ObservedGeneration: 2,
			Phase:              divergeiov1alpha1.PhaseRunning,
			Conditions: []metav1.Condition{{
				Type:               "ServicesReady",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 2,
				Reason:             "ServicesDeployed",
			}},
		},
	}
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pg-takeover",
			Finalizers: []string{previewGroupFinalizer},
			Annotations: map[string]string{
				annotationGroupDBNamespace: "default",
			},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "img:2", Database: &divergeiov1alpha1.EnvironmentDatabase{Mode: "fresh"}},
			},
		},
	}

	// Pre-upgrade legacy Secret that once carried previewgroup labels gets taken over by per-service ensureDatabaseEnvSecret.
	legacySec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      legacySecName,
			Namespace: "default",
			Labels: map[string]string{
				"divergedev.com/environment": childName,
				labelPreviewGroup:            "pg-takeover",
				"divergedev.com/managed-by":  "diverge",
				labelSharedGroupDBSecret:     "true",
			},
		},
		Data: map[string][]byte{"DATABASE_URL": []byte("postgres://old-shared")},
	}
	groupSec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      groupSecName,
			Namespace: "default",
			Labels: map[string]string{
				"divergedev.com/environment": childName,
				labelPreviewGroup:            "pg-takeover",
				"divergedev.com/managed-by":  "diverge",
				labelSharedGroupDBSecret:     "true",
			},
		},
		Data: map[string][]byte{"DATABASE_URL": []byte("postgres://old-shared")},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}).WithObjects(pg, child, legacySec, groupSec).Build()
	envRec := &EnvironmentReconciler{Client: c, Scheme: s}
	require.NoError(t, envRec.ensureDatabaseEnvSecret(ctx, child, map[string]string{"DATABASE_URL": "postgres://per-service-fresh"}))

	var updatedLegacy corev1.Secret
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: legacySecName, Namespace: "default"}, &updatedLegacy))
	assert.Empty(t, updatedLegacy.Labels[labelPreviewGroup], "ensureDatabaseEnvSecret must clear previewgroup label when taking over legacy Secret")
	assert.Empty(t, updatedLegacy.Labels[labelSharedGroupDBSecret], "ensureDatabaseEnvSecret must clear shared-group-db-secret label on takeover")
	assert.True(t, hasEnvironmentControllerOwnerRef(&updatedLegacy))

	dbProv := &countingGroupDBProvider{dsn: "postgres://unused"}
	r := &PreviewGroupReconciler{
		Client:           c,
		Scheme:           s,
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(10)),
		DatabaseProvider: dbProv,
	}

	inUse, useErr := r.childOrWorkloadStillUsesSharedDBSecret(ctx, pg.Name, child)
	require.NoError(t, useErr)
	assert.False(t, inUse, "ConnectionRef matching per-service Secret name must not be treated as shared DB usage")

	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-takeover"}})
	require.NoError(t, err)
	assert.Equal(t, int32(1), dbProv.teardowns.Load())
	var updatedTakeoverPG divergeiov1alpha1.PreviewGroup
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "pg-takeover"}, &updatedTakeoverPG))
	assert.Empty(t, updatedTakeoverPG.Annotations[annotationGroupDBNamespace])

	var deletedGroupSec corev1.Secret
	err = c.Get(ctx, types.NamespacedName{Name: groupSecName, Namespace: "default"}, &deletedGroupSec)
	assert.True(t, apierrors.IsNotFound(err), "group-owned Secret must be deleted")
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: legacySecName, Namespace: "default"}, &updatedLegacy), "per-service Secret must survive cleanup")
	assert.Equal(t, "postgres://per-service-fresh", string(updatedLegacy.Data["DATABASE_URL"]))
}

func TestSharedDB_UnrelatedFailingChildDoesNotBlockTeardown(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, appsv1.AddToScheme(s))

	apiChild := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        childEnvironmentName("pg-unrelated", "api"),
			Namespace:   "default",
			Generation:  2,
			Labels:      map[string]string{labelPreviewGroup: "pg-unrelated", labelManagedBy: "diverge-previewgroup"},
			Annotations: map[string]string{annotationSharedGroupDBDraining: "true"},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{Mode: "fresh"},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			ObservedGeneration: 2,
			Phase:              divergeiov1alpha1.PhaseRunning,
			Conditions: []metav1.Condition{{
				Type:               "ServicesReady",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 2,
				Reason:             "ServicesDeployed",
			}},
		},
	}
	// worker always had its own per-service database and currently fails image deploy.
	workerChild := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       childEnvironmentName("pg-unrelated", "worker"),
			Namespace:  "default",
			Generation: 2,
			Labels:     map[string]string{labelPreviewGroup: "pg-unrelated", labelManagedBy: "diverge-previewgroup"},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{Mode: "fresh"},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			ObservedGeneration: 1,
			Phase:              divergeiov1alpha1.PhaseFailed,
			Conditions: []metav1.Condition{{
				Type:               "ServicesReady",
				Status:             metav1.ConditionFalse,
				ObservedGeneration: 2,
				Reason:             "DeployFailed",
			}},
		},
	}
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "pg-unrelated",
			Finalizers:  []string{previewGroupFinalizer},
			Annotations: map[string]string{annotationGroupDBNamespace: "default"},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "api:2", Database: &divergeiov1alpha1.EnvironmentDatabase{Mode: "fresh"}},
				{Name: "worker", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "worker:bad", Database: &divergeiov1alpha1.EnvironmentDatabase{Mode: "fresh"}},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}).WithObjects(pg, apiChild, workerChild).Build()
	dbProv := &countingGroupDBProvider{dsn: "postgres://unused"}
	r := &PreviewGroupReconciler{
		Client:           c,
		Scheme:           s,
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(10)),
		DatabaseProvider: dbProv,
	}

	inUseAPI, useErr := r.childOrWorkloadStillUsesSharedDBSecret(ctx, pg.Name, apiChild)
	require.NoError(t, useErr)
	assert.False(t, inUseAPI)
	inUseWorker, useErr := r.childOrWorkloadStillUsesSharedDBSecret(ctx, pg.Name, workerChild)
	require.NoError(t, useErr)
	assert.False(t, inUseWorker, "unrelated non-Running child without shared-DB usage must not block teardown")

	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-unrelated"}})
	require.NoError(t, err)
	assert.Equal(t, int32(1), dbProv.teardowns.Load())
	var updatedUnrelatedPG divergeiov1alpha1.PreviewGroup
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "pg-unrelated"}, &updatedUnrelatedPG))
	assert.Empty(t, updatedUnrelatedPG.Annotations[annotationGroupDBNamespace])
}

func TestSharedDB_LegacyGroupOwnedSecretMigration(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, appsv1.AddToScheme(s))

	childName := childEnvironmentName("pg-legacy-mig", "api")
	legacySecName := legacySharedGroupDBSecretName(childName)
	legacyGroupSec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      legacySecName,
			Namespace: "default",
			Labels: map[string]string{
				"divergedev.com/environment": childName,
				labelPreviewGroup:            "pg-legacy-mig",
				"divergedev.com/managed-by":  "diverge",
			},
		},
		Data: map[string][]byte{"DATABASE_URL": []byte("postgres://legacy-group")},
	}
	child := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       childName,
			Namespace:  "default",
			Generation: 2,
			Labels:     map[string]string{labelPreviewGroup: "pg-legacy-mig", labelManagedBy: "diverge-previewgroup"},
			Annotations: map[string]string{
				annotationSharedGroupDB: "true",
			},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Database: divergeiov1alpha1.EnvironmentDatabase{Mode: "shared"},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			ObservedGeneration: 2,
			Phase:              divergeiov1alpha1.PhaseRunning,
			Conditions: []metav1.Condition{{
				Type:               "ServicesReady",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 2,
				Reason:             "ServicesDeployed",
			}},
		},
	}
	oldPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: childName + "-legacy-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:    "api",
				Image:   "img:1",
				EnvFrom: []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: legacySecName}}}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "pg-legacy-mig",
			Finalizers:  []string{previewGroupFinalizer},
			Annotations: map[string]string{annotationGroupDBNamespace: "default"},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "img:2", Database: &divergeiov1alpha1.EnvironmentDatabase{Mode: "fresh"}},
			},
		},
	}

	// Verify readSharedGroupDatabaseDSN falls back cleanly to legacy group Secret when ConnectionRef is unset.
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}, &corev1.Pod{}).WithObjects(pg, child, legacyGroupSec, oldPod).Build()
	envRec := &EnvironmentReconciler{Client: c, Scheme: s}
	dsn, ready, dsnErr := envRec.readSharedGroupDatabaseDSN(ctx, child)
	require.NoError(t, dsnErr)
	assert.True(t, ready)
	assert.Equal(t, "postgres://legacy-group", dsn)

	// Opt-out transition removes annotationSharedGroupDB; old Running Pod referencing legacy group Secret still holds teardown.
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: childName, Namespace: "default"}, child))
	delete(child.Annotations, annotationSharedGroupDB)
	require.NoError(t, c.Update(ctx, child))

	dbProv := &countingGroupDBProvider{dsn: "postgres://unused"}
	r := &PreviewGroupReconciler{
		Client:           c,
		Scheme:           s,
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(10)),
		DatabaseProvider: dbProv,
	}
	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-legacy-mig"}})
	require.NoError(t, err)
	assert.Greater(t, res.RequeueAfter, time.Duration(0))
	assert.Zero(t, dbProv.teardowns.Load(), "live Pod mounting legacy group-owned Secret must block teardown")
	var kept corev1.Secret
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: legacySecName, Namespace: "default"}, &kept))

	// Once old Pod terminates, teardown proceeds and legacy group-owned Secret is deleted.
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: oldPod.Name, Namespace: "default"}, oldPod))
	oldPod.Status.Phase = corev1.PodSucceeded
	require.NoError(t, c.Status().Update(ctx, oldPod))

	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-legacy-mig"}})
	require.NoError(t, err)
	assert.Equal(t, int32(1), dbProv.teardowns.Load())
	err = c.Get(ctx, types.NamespacedName{Name: legacySecName, Namespace: "default"}, &kept)
	assert.True(t, apierrors.IsNotFound(err), "legacy group-owned Secret must be deleted once Pod drains")
}

func TestSharedDB_LegacyTakeoverTracksOldAndTerminatingPods(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, appsv1.AddToScheme(s))

	pgName := "pg-takeover-uids"
	childName := childEnvironmentName(pgName, "api")
	legacySecName := legacySharedGroupDBSecretName(childName)

	child := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       childName,
			Namespace:  "default",
			Generation: 2,
			Labels: map[string]string{
				labelPreviewGroup: pgName,
				labelManagedBy:    "diverge-previewgroup",
			},
			Annotations: map[string]string{
				annotationSharedGroupDBDraining: "true",
			},
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Deploy:   divergeiov1alpha1.EnvironmentDeploy{Mode: "delta", Namespace: "same"},
			Database: divergeiov1alpha1.EnvironmentDatabase{Mode: "fresh", ConnectionRef: legacySecName},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase:              divergeiov1alpha1.PhaseRunning,
			ObservedGeneration: 2,
			Conditions: []metav1.Condition{{
				Type:               "ServicesReady",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 2,
				Reason:             "ServicesDeployed",
			}},
		},
	}

	legacyGroupSec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      legacySecName,
			Namespace: "default",
			Labels: map[string]string{
				labelPreviewGroup:           pgName,
				"divergedev.com/managed-by": "diverge",
			},
		},
		Data: map[string][]byte{"DATABASE_URL": []byte("postgres://old-shared")},
	}

	oldPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:       childName + "-old",
			Namespace:  "default",
			UID:        types.UID("pod-uid-old"),
			Finalizers: []string{"test.divergedev.com/hold"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:    "api",
				Image:   "img:1",
				EnvFrom: []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: legacySecName}}}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childName + "-new",
			Namespace: "default",
			UID:       types.UID("pod-uid-new"),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:    "api",
				Image:   "img:2",
				EnvFrom: []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: legacySecName}}}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	one := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       childName,
			Namespace:  "default",
			Generation: 2,
			Labels:     map[string]string{"divergedev.com/environment": childName},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:    "api",
						Image:   "img:2",
						EnvFrom: []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: legacySecName}}}},
					}},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 2,
			Replicas:           1,
			UpdatedReplicas:    1,
		},
	}

	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:        pgName,
			Finalizers:  []string{previewGroupFinalizer},
			Annotations: map[string]string{annotationGroupDBNamespace: "default"},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "img:2", Database: &divergeiov1alpha1.EnvironmentDatabase{Mode: "fresh"}},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}, &divergeiov1alpha1.Environment{}, &corev1.Pod{}, &appsv1.Deployment{}).
		WithObjects(pg, child, legacyGroupSec, oldPod, dep).
		Build()

	envRec := &EnvironmentReconciler{Client: c, Scheme: s}
	require.NoError(t, envRec.ensureDatabaseEnvSecret(ctx, child, map[string]string{"DATABASE_URL": "postgres://new-per-service"}))

	var tookOver corev1.Secret
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: legacySecName, Namespace: "default"}, &tookOver))
	assert.Equal(t, "postgres://new-per-service", string(tookOver.Data["DATABASE_URL"]))
	assert.Equal(t, "uids:pod-uid-old", tookOver.Annotations[annotationLegacyGroupDBDraining])
	assert.Equal(t, pgName, tookOver.Annotations[annotationLegacyGroupDBOwner])

	// Replacement Pod spins up and oldPod begins terminating while Deployment is converged
	require.NoError(t, c.Create(ctx, newPod))
	require.NoError(t, c.Delete(ctx, oldPod))

	dbProv := &countingGroupDBProvider{dsn: "postgres://unused"}
	r := &PreviewGroupReconciler{
		Client:           c,
		Scheme:           s,
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(10)),
		DatabaseProvider: dbProv,
	}

	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pgName}})
	require.NoError(t, err)
	assert.Greater(t, res.RequeueAfter, time.Duration(0))
	assert.Zero(t, dbProv.teardowns.Load(), "terminating old Pod must continue blocking shared DB teardown after Secret takeover")
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: legacySecName, Namespace: "default"}, &tookOver))
	assert.Equal(t, "postgres://new-per-service", string(tookOver.Data["DATABASE_URL"]), "per-service Secret must not be deleted")

	// Remove finalizer on oldPod so it exits; newPod remains Running on per-service Secret
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: oldPod.Name, Namespace: "default"}, oldPod))
	oldPod.Finalizers = nil
	require.NoError(t, c.Update(ctx, oldPod))

	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pgName}})
	require.NoError(t, err)
	assert.Equal(t, int32(1), dbProv.teardowns.Load(), "shared DB teardown must run once terminating legacy consumer exits")
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: legacySecName, Namespace: "default"}, &tookOver), "per-service Secret must remain intact after legacy draining finishes")
	assert.Empty(t, tookOver.Annotations[annotationLegacyGroupDBDraining])
	assert.Empty(t, tookOver.Annotations[annotationLegacyGroupDBOwner])
}

func TestSharedDB_OrphanedSecretWithGoneChildBlocksTeardownUntilPodExits(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	pgName := "pg-gone-child"
	childName := childEnvironmentName(pgName, "api")
	secName := sharedGroupDBSecretName(childName)

	orphanSec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secName,
			Namespace: "backend",
			Labels: map[string]string{
				labelPreviewGroup:           pgName,
				"divergedev.com/managed-by": "diverge",
				labelSharedGroupDBSecret:    "true",
			},
		},
		Data: map[string][]byte{"DATABASE_URL": []byte("postgres://shared")},
	}
	strayPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "stray-api-pod", Namespace: "backend"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:    "api",
				Image:   "img:1",
				EnvFrom: []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: secName}}}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	// A. Disabled shared DB mode with no child Environments still waits for live Pod referencing orphaned Secret
	pgDisabled := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:        pgName,
			Finalizers:  []string{previewGroupFinalizer},
			Annotations: map[string]string{annotationGroupDBNamespace: "default"},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "web", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "web:1"},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}, &corev1.Pod{}).
		WithObjects(pgDisabled, orphanSec, strayPod).
		Build()
	dbProv := &countingGroupDBProvider{dsn: "postgres://unused"}
	r := &PreviewGroupReconciler{
		Client:           c,
		Scheme:           s,
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(10)),
		DatabaseProvider: dbProv,
	}

	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pgName}})
	require.NoError(t, err)
	assert.Greater(t, res.RequeueAfter, time.Duration(0))
	assert.Zero(t, dbProv.teardowns.Load(), "provider teardown must be skipped while stray Pod uses orphan Secret")
	var kept corev1.Secret
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: secName, Namespace: "backend"}, &kept))

	// B. Deleting PreviewGroup with gone child Environment also blocks both Secret delete and DB teardown until Pod exits
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: pgName}, pgDisabled))
	require.NoError(t, c.Delete(ctx, pgDisabled))
	res, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pgName}})
	require.NoError(t, err)
	assert.Greater(t, res.RequeueAfter, time.Duration(0))
	assert.Zero(t, dbProv.teardowns.Load(), "group teardown must wait for stray Pod before tearing down DB")
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: secName, Namespace: "backend"}, &kept))

	// Once Pod completes, teardown deletes Secret, calls provider.Teardown, and releases finalizer
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: strayPod.Name, Namespace: "backend"}, strayPod))
	strayPod.Status.Phase = corev1.PodSucceeded
	require.NoError(t, c.Status().Update(ctx, strayPod))

	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pgName}})
	require.NoError(t, err)
	assert.Equal(t, int32(1), dbProv.teardowns.Load())
	err = c.Get(ctx, types.NamespacedName{Name: secName, Namespace: "backend"}, &kept)
	assert.True(t, apierrors.IsNotFound(err), "orphan Secret must be deleted once Pod completes")
	err = c.Get(ctx, types.NamespacedName{Name: pgName}, pgDisabled)
	assert.True(t, apierrors.IsNotFound(err), "PreviewGroup finalizer must be released after teardown")
}

func TestSharedDB_DeploymentListedBeforePods(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, appsv1.AddToScheme(s))

	var order []string
	c := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			switch list.(type) {
			case *appsv1.DeploymentList:
				order = append(order, "Deployment")
			case *corev1.PodList:
				order = append(order, "Pod")
			}
			return cl.List(ctx, list, opts...)
		},
	}).Build()

	inUse, err := namespaceWorkloadsStillUseSecret(ctx, c, "default", "child", []string{"diverge-group-db-env-child"}, false, nil, "")
	require.NoError(t, err)
	assert.False(t, inUse)
	assert.Equal(t, []string{"Deployment", "Pod"}, order, "Deployment list must run before Pod list")
}

func TestSharedDB_OrphanSecretDeletePreconditionsAndConflictRequeue(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	pgName := "pg-precond"
	secName := sharedGroupDBSecretName(childEnvironmentName(pgName, "api"))
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secName,
			Namespace: "default",
			UID:       types.UID("uid-sec-1"),
			Labels: map[string]string{
				labelPreviewGroup:           pgName,
				"divergedev.com/managed-by": "diverge",
				labelSharedGroupDBSecret:    "true",
			},
		},
	}
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:        pgName,
			Annotations: map[string]string{annotationGroupDBNamespace: "default"},
		},
	}

	var sawPrecond bool
	var injectConflict atomic.Bool
	injectConflict.Store(true)

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pg, sec).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if sObj, ok := obj.(*corev1.Secret); ok && sObj.Name == secName {
				delOpts := &client.DeleteOptions{}
				for _, o := range opts {
					o.ApplyToDelete(delOpts)
				}
				if delOpts.Preconditions != nil &&
					delOpts.Preconditions.UID != nil && *delOpts.Preconditions.UID == types.UID("uid-sec-1") &&
					delOpts.Preconditions.ResourceVersion != nil && *delOpts.Preconditions.ResourceVersion != "" {
					sawPrecond = true
				}
				if injectConflict.Swap(false) {
					return apierrors.NewConflict(corev1.Resource("secrets"), sObj.Name, errors.New("modified concurrently"))
				}
			}
			return cl.Delete(ctx, obj, opts...)
		},
	}).Build()

	r := &PreviewGroupReconciler{Client: c, Scheme: s}
	requeue, err := r.cleanupOrphanedChildSharedDBSecrets(ctx, pg, nil)
	require.NoError(t, err)
	assert.True(t, sawPrecond, "orphan Secret Delete must include UID and ResourceVersion preconditions")
	assert.True(t, requeue, "409 Conflict during orphan Secret Delete must trigger requeue without failing")
	var stillThere corev1.Secret
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: secName, Namespace: "default"}, &stillThere))

	requeue, err = r.cleanupOrphanedChildSharedDBSecrets(ctx, pg, nil)
	require.NoError(t, err)
	assert.False(t, requeue)
	err = c.Get(ctx, types.NamespacedName{Name: secName, Namespace: "default"}, &stillThere)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestSharedDB_RemoteSpokeTargetPersistedAcrossChildForceTeardown(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	pgName := "pg-remote-force"
	spokeRef := &divergeiov1alpha1.ClusterTargetRef{
		Name:                "spoke-api",
		KubeconfigSecretRef: &divergeiov1alpha1.SecretRef{Name: "spoke-api-kc", Namespace: "kube-system"},
	}
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       pgName,
			Labels:     map[string]string{previewgroup.LabelComposedGroup: "true"},
			Finalizers: []string{previewGroupFinalizer},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Database: &divergeiov1alpha1.EnvironmentDatabase{Mode: "cnpg"},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "backend", Mode: divergeiov1alpha1.ServiceModeImage, Image: "api:1", ClusterRef: spokeRef},
				{Name: "web", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "web:1"},
			},
		},
	}

	spokeClient := fake.NewClientBuilder().WithScheme(s).Build()
	hubClient := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}, &divergeiov1alpha1.Environment{}).WithObjects(pg).Build()
	dbProv := &countingGroupDBProvider{dsn: "postgres://app:pw@db.external.example.com:5432/app"}
	r := &PreviewGroupReconciler{
		Client:           hubClient,
		Scheme:           s,
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(10)),
		DatabaseProvider: dbProv,
		ClusterClients: &nsAwareSpokeResolver{
			byNamespace: map[string]*multicluster.Target{
				"backend": {Name: "spoke-api", IsRemote: true, Client: spokeClient},
			},
		},
	}

	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pgName}})
	require.NoError(t, err)

	apiSecName := sharedGroupDBSecretName(childEnvironmentName(pgName, "api"))
	var spokeSec corev1.Secret
	require.NoError(t, spokeClient.Get(ctx, types.NamespacedName{Name: apiSecName, Namespace: "backend"}, &spokeSec))

	var updatedPG divergeiov1alpha1.PreviewGroup
	require.NoError(t, hubClient.Get(ctx, types.NamespacedName{Name: pgName}, &updatedPG))
	assert.NotEmpty(t, updatedPG.Annotations[annotationGroupDBSecretTargets], "remote spoke target must be recorded before spoke Secret creation")

	// Simulate service removal from Spec AND child Environment force-deleted without spoke Secret cleanup
	apiChild := &divergeiov1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: childEnvironmentName(pgName, "api"), Namespace: "backend"}}
	require.NoError(t, hubClient.Delete(ctx, apiChild))
	updatedPG.Spec.Services = []divergeiov1alpha1.PreviewGroupServiceSpec{
		{Name: "web", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "web:1"},
	}
	require.NoError(t, hubClient.Update(ctx, &updatedPG))

	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pgName}})
	require.NoError(t, err)

	err = spokeClient.Get(ctx, types.NamespacedName{Name: apiSecName, Namespace: "backend"}, &spokeSec)
	assert.True(t, apierrors.IsNotFound(err), "recorded target must keep spoke reachable so orphan Secret is swept even after child disappearance")
	require.NoError(t, hubClient.Get(ctx, types.NamespacedName{Name: pgName}, &updatedPG))
	assert.Empty(t, updatedPG.Annotations[annotationGroupDBSecretTargets], "inactive remote spoke target must be pruned after clean sweep")
}

func TestPostDeployHook_GatedOnCurrentChildRevision(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, batchv1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	pgName := "pg-postdeploy-gate"
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       pgName,
			Finalizers: []string{previewGroupFinalizer},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{{
				Name:       "api",
				Namespace:  "default",
				Mode:       divergeiov1alpha1.ServiceModeImage,
				Image:      "img:v2",
				PostDeploy: &divergeiov1alpha1.PostDeploySpec{Image: "alpine:latest"},
			}},
		},
	}

	childName := childEnvironmentName(pgName, "api")
	rTmp := &PreviewGroupReconciler{}
	desiredSynced := rTmp.buildChildEnvironmentWithSharedDB(pg, pg.Spec.Services[0], childName, "default", false, "")
	desiredSynced.Generation = 2
	desiredSynced.Status = divergeiov1alpha1.EnvironmentStatus{
		Phase:              divergeiov1alpha1.PhaseRunning,
		ObservedGeneration: 1, // lags Generation=2
	}

	c := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}, &divergeiov1alpha1.Environment{}).
		WithObjects(pg, desiredSynced).
		Build()
	r := &PreviewGroupReconciler{
		Client:   c,
		Scheme:   s,
		Recorder: events.NewRecorder(k8sevents.NewFakeRecorder(10)),
	}

	// 1. ObservedGeneration < Generation -> no post-deploy Job created
	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pgName}})
	require.NoError(t, err)
	assert.Greater(t, res.RequeueAfter, time.Duration(0))
	var jobs batchv1.JobList
	require.NoError(t, c.List(ctx, &jobs, client.InNamespace("default")))
	assert.Empty(t, jobs.Items, "post-deploy hook must not run while child ObservedGeneration lags Generation")

	// 2. Child spec still needs update even if ObservedGeneration == Generation -> no post-deploy Job created
	var child divergeiov1alpha1.Environment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: childName, Namespace: "default"}, &child))
	child.Spec.ServiceConfig.Image = "img:v1"
	require.NoError(t, c.Update(ctx, &child))
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: childName, Namespace: "default"}, &child))
	child.Status.ObservedGeneration = child.Generation
	require.NoError(t, c.Status().Update(ctx, &child))

	res, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pgName}})
	require.NoError(t, err)
	assert.Greater(t, res.RequeueAfter, time.Duration(0))
	require.NoError(t, c.List(ctx, &jobs, client.InNamespace("default")))
	assert.Empty(t, jobs.Items, "post-deploy hook must not run on the reconcile that updates child spec")

	// 3. Converged (no pending spec update and ObservedGeneration >= Generation) -> creates post-deploy Job
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: pgName}})
	require.NoError(t, err)
	require.NoError(t, c.List(ctx, &jobs, client.InNamespace("default")))
	require.Len(t, jobs.Items, 1)
	assert.Equal(t, hookTypePostDeploy, jobs.Items[0].Labels[labelHookType])
}

func TestGroupDBFailedNotification_RetriesAfterSourcePromotionFailure(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(s))
	require.NoError(t, corev1.AddToScheme(s))

	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pg-fail-retry",
			Labels:     map[string]string{previewgroup.LabelComposedGroup: "true"},
			Finalizers: []string{previewGroupFinalizer},
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source:   divergeiov1alpha1.EnvironmentSource{Provider: "github", Project: "acme/mono", MR: 101, Branch: "feat/101"},
			Database: &divergeiov1alpha1.EnvironmentDatabase{Mode: "cnpg"},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "default", Mode: divergeiov1alpha1.ServiceModeImage, Image: "img:101"},
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			Phase:         divergeiov1alpha1.PreviewGroupPhaseFailed,
			CommentID:     888,
			CommentSource: "github:acme/mono#10",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}).WithObjects(pg).Build()
	notif := &mockNotifier{nextCommentID: 1001, failNextFailed: errors.New("transient 502")}
	r := &PreviewGroupReconciler{
		Client:           c,
		Scheme:           s,
		Recorder:         events.NewRecorder(k8sevents.NewFakeRecorder(10)),
		Notifier:         notif,
		DatabaseProvider: &failingProvDB{err: errors.New("cnpg quota exceeded")},
	}

	// Attempt 1: PostGroupFailed fails after source promotion -> CommentID cleared, prev CommentSource preserved for retry
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-fail-retry"}})
	require.Error(t, err)
	require.Len(t, notif.failedCalls, 1)
	var gotPG divergeiov1alpha1.PreviewGroup
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "pg-fail-retry"}, &gotPG))
	assert.Zero(t, gotPG.Status.CommentID)
	assert.Equal(t, "github:acme/mono#10", gotPG.Status.CommentSource)

	// Attempt 2: DB still fails (Phase unchanged), PostGroupFailed retried and succeeds -> new CommentID + promoted source persisted
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-fail-retry"}})
	require.Error(t, err)
	require.Len(t, notif.failedCalls, 2)
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "pg-fail-retry"}, &gotPG))
	assert.Equal(t, int64(1001), gotPG.Status.CommentID)
	assert.Equal(t, "github:acme/mono#101", gotPG.Status.CommentSource)

	// Attempt 3: promoted source now persisted -> PostGroupFailed is not called again
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-fail-retry"}})
	require.Error(t, err)
	assert.Len(t, notif.failedCalls, 2)
}
