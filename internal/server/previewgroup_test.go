package server

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"time"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server/auth"
)

func buildPreviewGroupTestSetup(t *testing.T) (client.Client, *fakek8s.Clientset, divergev1alpha1connect.PreviewGroupServiceHandler) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	k8s := fakek8s.NewSimpleClientset()
	k8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = true
		return true, sar, nil
	})

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	audit := NewAuditLogger(logger)
	svc := NewPreviewGroupService(c, k8s, nil, NewStreamLimiter(250, 20), logger, audit)
	return c, k8s, svc
}

func TestPreviewGroupService_CRUDAndFieldMask(t *testing.T) {
	c, _, svc := buildPreviewGroupTestSetup(t)
	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "test-user"})

	// 1. CreatePreviewGroup
	createRes, err := svc.CreatePreviewGroup(ctx, connect.NewRequest(&pb.CreatePreviewGroupRequest{
		Namespace: "default",
		PreviewGroup: &pb.PreviewGroup{
			Name:        "pg-payments",
			Labels:      map[string]string{"team": "checkout"},
			Annotations: map[string]string{"note": "initial"},
			Spec: &pb.PreviewGroupSpec{
				Owner: "test-user",
			},
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, createRes.Msg.PreviewGroup)
	assert.Equal(t, "pg-payments", createRes.Msg.PreviewGroup.Name)
	assert.Equal(t, "default", createRes.Msg.PreviewGroup.Namespace)
	assert.Equal(t, "checkout", createRes.Msg.PreviewGroup.Labels["team"])

	// 2. GetPreviewGroup
	getRes, err := svc.GetPreviewGroup(ctx, connect.NewRequest(&pb.GetPreviewGroupRequest{
		Name:      "pg-payments",
		Namespace: "default",
	}))
	require.NoError(t, err)
	assert.Equal(t, "pg-payments", getRes.Msg.PreviewGroup.Name)
	rv := getRes.Msg.PreviewGroup.ResourceVersion
	require.NotEmpty(t, rv)

	// 3. ListPreviewGroups with label selector
	listRes, err := svc.ListPreviewGroups(ctx, connect.NewRequest(&pb.ListPreviewGroupsRequest{
		Namespace:     "default",
		LabelSelector: "team=checkout",
	}))
	require.NoError(t, err)
	require.Len(t, listRes.Msg.PreviewGroups, 1)
	assert.Equal(t, "pg-payments", listRes.Msg.PreviewGroups[0].Name)

	// 4. UpdatePreviewGroup without ResourceVersion -> CodeInvalidArgument
	_, err = svc.UpdatePreviewGroup(ctx, connect.NewRequest(&pb.UpdatePreviewGroupRequest{
		PreviewGroup: &pb.PreviewGroup{
			Name:      "pg-payments",
			Namespace: "default",
		},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// 5. UpdatePreviewGroup with unsupported field mask path -> CodeInvalidArgument
	_, err = svc.UpdatePreviewGroup(ctx, connect.NewRequest(&pb.UpdatePreviewGroupRequest{
		PreviewGroup: &pb.PreviewGroup{
			Name:            "pg-payments",
			Namespace:       "default",
			ResourceVersion: rv,
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.owner"}},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// 6. UpdatePreviewGroup partial update with UpdateMask=["labels"] preserves annotations
	updRes, err := svc.UpdatePreviewGroup(ctx, connect.NewRequest(&pb.UpdatePreviewGroupRequest{
		PreviewGroup: &pb.PreviewGroup{
			Name:            "pg-payments",
			Namespace:       "default",
			ResourceVersion: rv,
			Labels:          map[string]string{"team": "payments-v2"},
			Annotations:     map[string]string{"note": "should-be-ignored"},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"labels"}},
	}))
	require.NoError(t, err)
	assert.Equal(t, "payments-v2", updRes.Msg.PreviewGroup.Labels["team"])
	assert.Equal(t, "initial", updRes.Msg.PreviewGroup.Annotations["note"])

	var stored v1alpha1.PreviewGroup
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "default", Name: "pg-payments"}, &stored))
	assert.Equal(t, "payments-v2", stored.Labels["team"])
	assert.Equal(t, "initial", stored.Annotations["note"])

	// 7. DeletePreviewGroup
	_, err = svc.DeletePreviewGroup(ctx, connect.NewRequest(&pb.DeletePreviewGroupRequest{
		Name:      "pg-payments",
		Namespace: "default",
	}))
	require.NoError(t, err)

	// 8. Get after delete returns CodeNotFound
	_, err = svc.GetPreviewGroup(ctx, connect.NewRequest(&pb.GetPreviewGroupRequest{
		Name:      "pg-payments",
		Namespace: "default",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestPreviewGroupService_ValidationAndWatchUnimplemented(t *testing.T) {
	_, _, svc := buildPreviewGroupTestSetup(t)
	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "test-user"})

	// Nil PreviewGroup
	_, err := svc.CreatePreviewGroup(ctx, connect.NewRequest(&pb.CreatePreviewGroupRequest{
		Namespace: "default",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// Namespace mismatch
	_, err = svc.CreatePreviewGroup(ctx, connect.NewRequest(&pb.CreatePreviewGroupRequest{
		Namespace: "ns-a",
		PreviewGroup: &pb.PreviewGroup{
			Name:      "pg-1",
			Namespace: "ns-b",
		},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// Overlong page_token / label_selector in ListPreviewGroups
	_, err = svc.ListPreviewGroups(ctx, connect.NewRequest(&pb.ListPreviewGroupsRequest{
		Namespace: "default",
		PageToken: strings.Repeat("a", 4097),
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = svc.ListPreviewGroups(ctx, connect.NewRequest(&pb.ListPreviewGroupsRequest{
		Namespace:     "default",
		LabelSelector: "===invalid",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// WatchPreviewGroups when informerMgr is nil -> CodeUnimplemented
	err = svc.WatchPreviewGroups(ctx, connect.NewRequest(&pb.WatchPreviewGroupsRequest{Namespace: "default"}), nil)
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

func TestPreviewGroupService_CrossNamespaceServiceAuthz(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	k8s := fakek8s.NewSimpleClientset()
	k8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		attrs := sar.Spec.ResourceAttributes
		// Allow actions in "team-a", deny in "restricted-ns"
		if attrs != nil && attrs.Namespace == "restricted-ns" {
			sar.Status.Allowed = false
			return true, sar, nil
		}
		sar.Status.Allowed = true
		return true, sar, nil
	})

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	audit := NewAuditLogger(logger)
	svc := NewPreviewGroupService(c, k8s, nil, NewStreamLimiter(250, 20), logger, audit)
	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "alice"})

	// Creating a PreviewGroup in "team-a" referencing a service in "restricted-ns" is rejected with CodePermissionDenied
	_, err := svc.CreatePreviewGroup(ctx, connect.NewRequest(&pb.CreatePreviewGroupRequest{
		Namespace: "team-a",
		PreviewGroup: &pb.PreviewGroup{
			Name: "pg-cross-ns",
			Spec: &pb.PreviewGroupSpec{
				Services: []*pb.PreviewGroupServiceSpec{
					{
						Name:      "billing",
						Namespace: "restricted-ns",
					},
				},
			},
		},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "permission denied for service namespace restricted-ns")
	assert.Contains(t, buf.String(), "authz.denied")

	// Creating a PreviewGroup with services in the same namespace ("team-a") succeeds
	createRes, err := svc.CreatePreviewGroup(ctx, connect.NewRequest(&pb.CreatePreviewGroupRequest{
		Namespace: "team-a",
		PreviewGroup: &pb.PreviewGroup{
			Name: "pg-same-ns",
			Spec: &pb.PreviewGroupSpec{
				Services: []*pb.PreviewGroupServiceSpec{
					{
						Name:      "frontend",
						Namespace: "team-a",
					},
				},
			},
		},
	}))
	require.NoError(t, err)
	rv := createRes.Msg.PreviewGroup.ResourceVersion

	// Updating that PreviewGroup to reference "restricted-ns" is also rejected with CodePermissionDenied
	_, err = svc.UpdatePreviewGroup(ctx, connect.NewRequest(&pb.UpdatePreviewGroupRequest{
		PreviewGroup: &pb.PreviewGroup{
			Name:            "pg-same-ns",
			Namespace:       "team-a",
			ResourceVersion: rv,
			Spec: &pb.PreviewGroupSpec{
				Services: []*pb.PreviewGroupServiceSpec{
					{
						Name:      "billing",
						Namespace: "restricted-ns",
					},
				},
			},
		},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "permission denied for service namespace restricted-ns")
}

func TestPreviewGroupService_UpdateRejectsClusterRefChangeOnExistingService(t *testing.T) {
	_, _, svc := buildPreviewGroupTestSetup(t)
	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "test-user"})

	created, err := svc.CreatePreviewGroup(ctx, connect.NewRequest(&pb.CreatePreviewGroupRequest{
		Namespace: "default",
		PreviewGroup: &pb.PreviewGroup{
			Name: "pg-federated",
			Spec: &pb.PreviewGroupSpec{
				ClusterRef: &pb.ClusterTargetRef{
					Name:                "us-west",
					KubeconfigSecretRef: &pb.SecretRef{Name: "west-sec"},
				},
				Services: []*pb.PreviewGroupServiceSpec{
					{Name: "orders", Image: "orders:v1"},
				},
			},
		},
	}))
	require.NoError(t, err)
	rv := created.Msg.PreviewGroup.ResourceVersion

	_, err = svc.UpdatePreviewGroup(ctx, connect.NewRequest(&pb.UpdatePreviewGroupRequest{
		PreviewGroup: &pb.PreviewGroup{
			Name:            "pg-federated",
			Namespace:       "default",
			ResourceVersion: rv,
			Spec: &pb.PreviewGroupSpec{
				ClusterRef: &pb.ClusterTargetRef{
					Name:                "us-east",
					KubeconfigSecretRef: &pb.SecretRef{Name: "east-sec"},
				},
				Services: []*pb.PreviewGroupServiceSpec{
					{Name: "orders", Image: "orders:v2"},
				},
			},
		},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestPreviewGroupService_ClusterScopedAndTTLRoundTrip(t *testing.T) {
	c, _, svc := buildPreviewGroupTestSetup(t)
	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "test-user"})

	// Cluster-scoped PreviewGroup created by CLI/Controller (empty Namespace) with a 24h TTL
	clusterPG := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "feat-checkout",
			Annotations: map[string]string{
				"divergedev.com/group-members": `[{"service":"web-app","project":"acme/web","mr":101}]`,
			},
		},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "acme/web",
				Branch:   "feat/checkout",
				MR:       101,
			},
			Routing: v1alpha1.PreviewGroupRouting{
				Mode:        "header",
				HeaderKey:   "x-diverge-env",
				HeaderValue: "feat-checkout",
			},
			Lifecycle: &v1alpha1.PreviewGroupLifecycle{
				TTL: &metav1.Duration{Duration: 24 * time.Hour},
			},
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{Name: "web-app", Mode: v1alpha1.ServiceModeImage, Image: "ghcr.io/acme/web:pr-101"},
			},
		},
	}
	require.NoError(t, c.Create(ctx, clusterPG))

	// GetPreviewGroup works with empty namespace ("") AND "_cluster" AND preserves 24h TTL
	got, err := svc.GetPreviewGroup(ctx, connect.NewRequest(&pb.GetPreviewGroupRequest{
		Name:      "feat-checkout",
		Namespace: "",
	}))
	require.NoError(t, err)
	require.NotNil(t, got.Msg.PreviewGroup.Spec.Lifecycle.GetTtl())
	assert.Equal(t, int64(86400), got.Msg.PreviewGroup.Spec.Lifecycle.GetTtl().GetSeconds())
	assert.Contains(t, got.Msg.PreviewGroup.Annotations["divergedev.com/group-members"], "web-app")

	// UpdatePreviewGroup works against cluster-scoped resource
	upd, err := svc.UpdatePreviewGroup(ctx, connect.NewRequest(&pb.UpdatePreviewGroupRequest{
		PreviewGroup: &pb.PreviewGroup{
			Name:            "feat-checkout",
			Namespace:       "",
			ResourceVersion: got.Msg.PreviewGroup.ResourceVersion,
			Annotations:     got.Msg.PreviewGroup.Annotations,
			Spec: &pb.PreviewGroupSpec{
				Source:  got.Msg.PreviewGroup.Spec.Source,
				Routing: got.Msg.PreviewGroup.Spec.Routing,
				Lifecycle: &pb.PreviewGroupLifecycle{
					Ttl: durationpb.New(12 * time.Hour),
				},
				Services: append(got.Msg.PreviewGroup.Spec.Services, &pb.PreviewGroupServiceSpec{
					Name:  "payments-api",
					Mode:  "image",
					Image: "ghcr.io/acme/payments:pr-42",
				}),
			},
		},
	}))
	require.NoError(t, err)
	assert.Len(t, upd.Msg.PreviewGroup.Spec.Services, 2)
	assert.Equal(t, int64(43200), upd.Msg.PreviewGroup.Spec.Lifecycle.GetTtl().GetSeconds())

	// DeletePreviewGroup works with "_cluster" sentinel
	_, err = svc.DeletePreviewGroup(ctx, connect.NewRequest(&pb.DeletePreviewGroupRequest{
		Name:      "feat-checkout",
		Namespace: "_cluster",
	}))
	require.NoError(t, err)
}

func TestPreviewGroupService_MacaroonSandboxEscapeAndCrossNamespaceDeleteBlocked(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	otherGroup := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-pg"},
		Spec: v1alpha1.PreviewGroupSpec{
			TopologyOverrides: []v1alpha1.TopologyEdge{{From: "web", To: "api", Protocol: "http"}},
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Namespace: "prod-payments", Mode: v1alpha1.ServiceModeImage, Image: "acme/api:v1"},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(otherGroup).Build()
	k8s := fakek8s.NewSimpleClientset()
	k8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = sar.Spec.ResourceAttributes != nil && sar.Spec.ResourceAttributes.Namespace == "sandbox-a"
		return true, sar, nil
	})

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	svc := NewPreviewGroupService(c, k8s, nil, NewStreamLimiter(250, 20), logger, NewAuditLogger(logger))

	macaroonCtx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{
		Username: "agent-task:t1",
		Extra: map[string]authorizationv1.ExtraValue{
			auth.ExtraKeyAuthType:     {"macaroon"},
			auth.ExtraKeyNamespace:    {"sandbox-a"},
			auth.ExtraKeyAllowedTools: {"diverge_join_preview_group", "diverge_leave_preview_group"},
		},
	})

	// 1. Empty service.namespace defaults to "default", blocking Macaroon sandbox escape out of "sandbox-a"
	_, err := svc.CreatePreviewGroup(macaroonCtx, connect.NewRequest(&pb.CreatePreviewGroupRequest{
		Namespace: "sandbox-a",
		PreviewGroup: &pb.PreviewGroup{
			Name: "escape-pg",
			Spec: &pb.PreviewGroupSpec{
				Services: []*pb.PreviewGroupServiceSpec{
					{Name: "api", Image: "attacker/payload:v1", Namespace: ""},
				},
			},
		},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// 2. Macaroon in "sandbox-a" cannot delete cluster-scoped PreviewGroup running in "prod-payments"
	_, err = svc.DeletePreviewGroup(macaroonCtx, connect.NewRequest(&pb.DeletePreviewGroupRequest{
		Name:      "prod-pg",
		Namespace: "sandbox-a",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// 3. TopologyOverrides preserved + stale If-Match rejected with CodeAborted for authorized admin
	adminK8s := fakek8s.NewSimpleClientset()
	adminK8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = true
		return true, sar, nil
	})
	adminSvc := NewPreviewGroupService(c, adminK8s, nil, NewStreamLimiter(250, 20), logger, NewAuditLogger(logger))
	adminCtx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "admin"})

	got, err := adminSvc.GetPreviewGroup(adminCtx, connect.NewRequest(&pb.GetPreviewGroupRequest{Name: "prod-pg"}))
	require.NoError(t, err)
	upd, err := adminSvc.UpdatePreviewGroup(adminCtx, connect.NewRequest(&pb.UpdatePreviewGroupRequest{
		PreviewGroup: &pb.PreviewGroup{
			Name:            "prod-pg",
			ResourceVersion: got.Msg.PreviewGroup.ResourceVersion,
			Spec: &pb.PreviewGroupSpec{
				Services: got.Msg.PreviewGroup.Spec.GetServices(),
			},
		},
	}))
	require.NoError(t, err)

	var stored v1alpha1.PreviewGroup
	require.NoError(t, c.Get(adminCtx, client.ObjectKey{Name: "prod-pg"}, &stored))
	require.Len(t, stored.Spec.TopologyOverrides, 1, "UpdatePreviewGroup must preserve CRD TopologyOverrides")

	delReq := connect.NewRequest(&pb.DeletePreviewGroupRequest{Name: "prod-pg"})
	delReq.Header().Set(IfMatchHeader, "stale-rv")
	_, err = adminSvc.DeletePreviewGroup(adminCtx, delReq)
	require.Error(t, err)
	assert.Equal(t, connect.CodeAborted, connect.CodeOf(err))

	delReqOK := connect.NewRequest(&pb.DeletePreviewGroupRequest{Name: "prod-pg"})
	delReqOK.Header().Set(IfMatchHeader, upd.Msg.PreviewGroup.ResourceVersion)
	_, err = adminSvc.DeletePreviewGroup(adminCtx, delReqOK)
	require.NoError(t, err)

	// 4. Cluster-scoped ListPreviewGroups(namespace="sandbox-a"), non-default RBAC Get/Detach,
	//    and blocking cross-namespace TTL assassination / group-members spoofing (P0-1, P0-2, P0-4, P1-4, P2-3)
	sharedGroup := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "shared-pg", // cluster-scoped: Namespace == ""
			Annotations: map[string]string{
				"divergedev.com/group-members": `[{"service":"web","namespace":"sandbox-a","provider":"github","project":"acme/web","mr":10},{"service":"api","namespace":"prod-payments","provider":"github","project":"acme/api","mr":20}]`,
			},
		},
		Spec: v1alpha1.PreviewGroupSpec{
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{Name: "web", Namespace: "sandbox-a", Mode: v1alpha1.ServiceModeImage, Image: "acme/web:v1"},
				{Name: "api", Namespace: "prod-payments", Mode: v1alpha1.ServiceModeImage, Image: "acme/api:v1"},
			},
		},
	}
	pureSandboxGroup := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "sandbox-only-pg"},
		Spec: v1alpha1.PreviewGroupSpec{
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{Name: "worker", Namespace: "sandbox-a", Mode: v1alpha1.ServiceModeImage, Image: "acme/worker:v1"},
			},
		},
	}
	require.NoError(t, c.Create(adminCtx, sharedGroup))
	require.NoError(t, c.Create(adminCtx, pureSandboxGroup))

	teamUserCtx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "alice-sandbox-a"})

	// Non-default namespace user lists cluster-scoped groups in "sandbox-a" -> sees BOTH shared-pg and sandbox-only-pg
	teamList, err := svc.ListPreviewGroups(teamUserCtx, connect.NewRequest(&pb.ListPreviewGroupsRequest{Namespace: "sandbox-a"}))
	require.NoError(t, err)
	assert.Len(t, teamList.Msg.PreviewGroups, 2)

	// Macaroon bound to "sandbox-a" ONLY sees sandbox-only-pg (multi-namespace shared-pg is filtered out!)
	macaroonListCtx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{
		Username: "agent-task:t1",
		Extra: map[string]authorizationv1.ExtraValue{
			auth.ExtraKeyAuthType:     {"macaroon"},
			auth.ExtraKeyNamespace:    {"sandbox-a"},
			auth.ExtraKeyAllowedTools: {"diverge_list_preview_groups"},
		},
	})
	macList, err := svc.ListPreviewGroups(macaroonListCtx, connect.NewRequest(&pb.ListPreviewGroupsRequest{Namespace: "sandbox-a"}))
	require.NoError(t, err)
	require.Len(t, macList.Msg.PreviewGroups, 1)
	assert.Equal(t, "sandbox-only-pg", macList.Msg.PreviewGroups[0].Name)

	// Non-default namespace user can GetPreviewGroup(Namespace: "") without "default" RBAC!
	teamGot, err := svc.GetPreviewGroup(teamUserCtx, connect.NewRequest(&pb.GetPreviewGroupRequest{Name: "shared-pg"}))
	require.NoError(t, err)

	// P0-1a: Non-default namespace user CANNOT assassinate shared-pg by setting Lifecycle.TTL=1s while prod-payments is present
	assassinateProto := teamGot.Msg.PreviewGroup
	assassinateClone, err := ProtoPgToCRD(assassinateProto)
	require.NoError(t, err)
	d1s := metav1.Duration{Duration: 1e9}
	assassinateClone.Spec.Lifecycle = &v1alpha1.PreviewGroupLifecycle{TTL: &d1s}
	assassinateReqProto, err := CRDPgToProto(assassinateClone)
	require.NoError(t, err)
	_, err = svc.UpdatePreviewGroup(teamUserCtx, connect.NewRequest(&pb.UpdatePreviewGroupRequest{
		PreviewGroup: assassinateReqProto,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// P0-1b: Non-default namespace user CANNOT hijack group-members ownership of "api" in prod-payments
	spoofProto, err := CRDPgToProto(sharedGroup)
	require.NoError(t, err)
	spoofProto.ResourceVersion = teamGot.Msg.PreviewGroup.ResourceVersion
	spoofProto.Annotations = map[string]string{
		"divergedev.com/group-members": `[{"service":"web","namespace":"sandbox-a","provider":"github","project":"acme/web","mr":10},{"service":"api","namespace":"sandbox-a","provider":"github","project":"acme/web","mr":10}]`,
	}
	_, err = svc.UpdatePreviewGroup(teamUserCtx, connect.NewRequest(&pb.UpdatePreviewGroupRequest{
		PreviewGroup: spoofProto,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// P1-4: Non-default namespace user CAN cleanly detach their own "web" service in "sandbox-a" with Namespace: ""
	detachProto, err := CRDPgToProto(sharedGroup)
	require.NoError(t, err)
	detachProto.ResourceVersion = teamGot.Msg.PreviewGroup.ResourceVersion
	detachProto.Spec.Services = detachProto.Spec.Services[1:] // keep only prod-payments "api" untouched
	detachProto.Annotations = map[string]string{
		"divergedev.com/group-members": `[{"service":"api","namespace":"prod-payments","provider":"github","project":"acme/api","mr":20}]`,
	}
	_, err = svc.UpdatePreviewGroup(teamUserCtx, connect.NewRequest(&pb.UpdatePreviewGroupRequest{
		PreviewGroup: detachProto,
	}))
	require.NoError(t, err)

	// P2-3: Missing image on mode=image service is rejected with CodeInvalidArgument
	_, err = svc.CreatePreviewGroup(teamUserCtx, connect.NewRequest(&pb.CreatePreviewGroupRequest{
		Namespace: "sandbox-a",
		PreviewGroup: &pb.PreviewGroup{
			Name: "invalid-svc-pg",
			Spec: &pb.PreviewGroupSpec{
				Services: []*pb.PreviewGroupServiceSpec{
					{Name: "web", Namespace: "sandbox-a", Mode: "image", Image: ""},
				},
			},
		},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
