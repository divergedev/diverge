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

	// Reconcile 1: child Generation=2 > ObservedGeneration=1 keeps standalone running; StatusReporter error triggers requeue without marking success reported
	resGen1, err := rGen.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-gen-check"}})
	require.NoError(t, err)
	assert.Equal(t, 1*time.Second, resGen1.RequeueAfter)
	assert.Equal(t, []string{"success"}, reporter.calls)

	var checkStandalone divergeiov1alpha1.Environment
	require.NoError(t, cGen.Get(ctx, types.NamespacedName{Name: "pr-77", Namespace: "default"}, &checkStandalone))
	assert.Empty(t, checkStandalone.Annotations[metrics.AnnotationHibernated], "must not hibernate standalone env on stale child ObservedGeneration")

	// Bring ObservedGeneration up to date and reconcile again -> hibernates standalone env AND retries "success" commit status!
	require.NoError(t, cGen.Get(ctx, types.NamespacedName{Name: desiredChild.Name, Namespace: "default"}, desiredChild))
	desiredChild.Status.ObservedGeneration = desiredChild.Generation
	require.NoError(t, cGen.Update(ctx, desiredChild))

	_, err = rGen.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "pg-gen-check"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"success", "success"}, reporter.calls)
	require.NoError(t, cGen.Get(ctx, types.NamespacedName{Name: "pr-77", Namespace: "default"}, &checkStandalone))
	assert.Equal(t, "true", checkStandalone.Annotations[metrics.AnnotationHibernated])

	// C. ensureAtlasCR preserves DSN Secret when Create returns AlreadyExists/timeout while CR exists
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
	getCount := 0
	interceptedAtlas := interceptor.NewClient(baseAtlasClient, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == "AtlasMigration" {
				getCount++
				if getCount == 1 {
					return apierrors.NewNotFound(schema.GroupResource{Group: "db.atlasgo.io", Resource: "atlasmigrations"}, key.Name)
				}
				return nil // simulates timeout where apiserver created the CR before error
			}
			return c.Get(ctx, key, obj, opts...)
		},
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == "AtlasMigration" {
				return errors.New("context deadline exceeded after write")
			}
			return c.Create(ctx, obj, opts...)
		},
	})
	envRec := &EnvironmentReconciler{Client: interceptedAtlas, Scheme: s}
	err = envRec.ensureAtlasCR(ctx, atlasEnv, &pkgdb.DatabaseResult{DSN: "postgres://u:p@host:5432/db"})
	require.Error(t, err)
	var dsnSecs corev1.SecretList
	require.NoError(t, baseAtlasClient.List(ctx, &dsnSecs, client.InNamespace("default")))
	assert.Len(t, dsnSecs.Items, 1, "DSN Secret must not be deleted when Atlas CR exists on re-read")
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
