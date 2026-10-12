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
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
