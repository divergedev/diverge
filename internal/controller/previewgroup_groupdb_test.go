package controller

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sevents "k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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

	// Cross-namespace child Secret gets FQDN rewrite to backend.svc.cluster.local
	ordSecretName := sanitizeK8sName("diverge-db-env-"+ordChildName, ordChildName, 63)
	var ordSec corev1.Secret
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: ordSecretName, Namespace: "orders"}, &ordSec))
	assert.Contains(t, string(ordSec.Data["DATABASE_URL"]), "@"+cnpgHost+".backend.svc.cluster.local:5432")

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
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			Phase:     divergeiov1alpha1.PreviewGroupPhasePending,
			CommentID: 99999, // old comment ID from closed PR #12!
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&divergeiov1alpha1.PreviewGroup{}).WithObjects(standaloneWeb, pg).Build()
	notif := &mockNotifier{}
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

	// 2. Second reconcile sees CNPG Ready, hibernates standaloneWeb, and resets CommentID from closed PR #12!
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "monorepo-group"}})
	require.NoError(t, err)

	var currentWeb divergeiov1alpha1.Environment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "pr-web-101", Namespace: "frontend"}, &currentWeb))
	assert.Equal(t, "true", currentWeb.Annotations[metrics.AnnotationHibernated])
	assert.Equal(t, "monorepo-group", currentWeb.Annotations[previewgroup.AnnotationAttachedPreviewGroup])
	assert.NotEmpty(t, notif.createdCalls, "source promotion in monorepo must reset stale CommentID and post fresh comment on PR #101")

	// 3. Delete PreviewGroup -> after teardown completes, standaloneWeb is automatically woken back up!
	var currentPG divergeiov1alpha1.PreviewGroup
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
