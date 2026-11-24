package controller

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	ordSecretName := sanitizeK8sName("diverge-db-env-"+ordChildName, ordChildName, 63)
	var ordSec corev1.Secret
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: ordSecretName, Namespace: "orders"}, &ordSec))
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
	apiSecretName := sanitizeK8sName("diverge-db-env-"+apiChildName, apiChildName, 63)
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
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "nb-mig-group"}, &currentPG))
	require.NoError(t, c.Delete(ctx, &currentPG))
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "nb-mig-group"}})
	require.NoError(t, err)

	orphanSecretName := sanitizeK8sName("diverge-db-env-"+childName, childName, 63)
	var orphanSec corev1.Secret
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

	// 3. Switch-off shared DB preserves diverge-db-env-* Secret until child workload ObservedGeneration catches up,
	//    AND spoke cleanup resolves inherited clusterRef in child's svcNS (not anchorNS).
	switchChildName := childEnvironmentName("pg-switch", "api")
	switchSecName := sanitizeK8sName("diverge-db-env-"+switchChildName, switchChildName, 63)
	switchChild := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        switchChildName,
			Namespace:   "backend",
			Generation:  3,
			Labels:      map[string]string{labelPreviewGroup: "pg-switch", labelManagedBy: "diverge-previewgroup"},
			Annotations: map[string]string{}, // switched off shared DB annotation already
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
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
			ClusterRef: &divergeiov1alpha1.ClusterTargetRef{
				Name:                "spoke-a",
				KubeconfigSecretRef: &divergeiov1alpha1.SecretRef{Name: "kubeconfig"},
			},
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
			Name:       "deploy-guard-env",
			Namespace:  "default",
			Generation: 2,
			Finalizers: []string{environmentFinalizer},
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
	assert.True(t, childStillUsesSharedDBSecret(&updated, "diverge-db-env-deploy-guard-env"))

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
	assert.False(t, childStillUsesSharedDBSecret(&updated, "diverge-db-env-deploy-guard-env"))
}
