package server

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/divergedev/diverge/internal/server/auth"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	coretesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
)

type interceptingClient struct {
	client.Client
	opts []client.ListOption
}

func (i *interceptingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	i.opts = opts
	return i.Client.List(ctx, list, opts...)
}

func setupTestEnvs(t *testing.T, count int) client.Client {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	var objs []client.Object
	for i := 0; i < count; i++ {
		objs = append(objs, &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("env-%d", i),
				Namespace: "default",
				Labels: map[string]string{
					"test": "true",
					"mod":  fmt.Sprintf("%d", i%2),
				},
			},
		})
	}

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestListEnvironments_Pagination(t *testing.T) {
	totalCount := 250
	c := setupTestEnvs(t, totalCount)
	k8sClient := k8sfake.NewSimpleClientset()
	k8sClient.PrependReactor("create", "subjectaccessreviews", func(action coretesting.Action) (handled bool, ret runtime.Object, err error) {
		sar := action.(coretesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = true
		return true, sar, nil
	})

	ic := &interceptingClient{Client: c}
	svc := NewEnvironmentService(ic, k8sClient, nil, nil, nil, slog.Default(), NewAuditLogger(slog.Default()))

	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{
		Username: "test-user",
		UID:      "u-123",
		Groups:   []string{"system:masters"}, // bypass RBAC usually
	})

	t.Run("default page size is applied", func(t *testing.T) {
		req := connect.NewRequest(&pb.ListEnvironmentsRequest{
			Namespace: "default",
		})
		_, err := svc.ListEnvironments(ctx, req)
		require.NoError(t, err)

		listOpts := &client.ListOptions{}
		for _, opt := range ic.opts {
			opt.ApplyToList(listOpts)
		}
		require.Equal(t, int64(100), listOpts.Limit)
		require.Equal(t, "default", listOpts.Namespace)
	})

	t.Run("page size is respected and capped at 1000", func(t *testing.T) {
		req := connect.NewRequest(&pb.ListEnvironmentsRequest{
			Namespace: "default",
			PageSize:  50,
		})
		_, err := svc.ListEnvironments(ctx, req)
		require.NoError(t, err)

		listOpts := &client.ListOptions{}
		for _, opt := range ic.opts {
			opt.ApplyToList(listOpts)
		}
		require.Equal(t, int64(50), listOpts.Limit)

		reqCap := connect.NewRequest(&pb.ListEnvironmentsRequest{
			Namespace: "default",
			PageSize:  2000,
		})
		_, err = svc.ListEnvironments(ctx, reqCap)
		require.NoError(t, err)

		listOptsCap := &client.ListOptions{}
		for _, opt := range ic.opts {
			opt.ApplyToList(listOptsCap)
		}
		require.Equal(t, int64(1000), listOptsCap.Limit)
	})

	t.Run("continue token is applied", func(t *testing.T) {
		req := connect.NewRequest(&pb.ListEnvironmentsRequest{
			Namespace: "default",
			PageToken: "test-continue-token",
		})
		_, err := svc.ListEnvironments(ctx, req)
		require.NoError(t, err)

		listOpts := &client.ListOptions{}
		for _, opt := range ic.opts {
			opt.ApplyToList(listOpts)
		}
		require.Equal(t, "test-continue-token", listOpts.Continue)
	})

	t.Run("label selector sets matching labels", func(t *testing.T) {
		req := connect.NewRequest(&pb.ListEnvironmentsRequest{
			Namespace:     "default",
			LabelSelector: "mod=0",
		})
		_, err := svc.ListEnvironments(ctx, req)
		require.NoError(t, err)

		listOpts := &client.ListOptions{}
		for _, opt := range ic.opts {
			opt.ApplyToList(listOpts)
		}
		require.NotNil(t, listOpts.LabelSelector)
		require.Equal(t, "mod=0", listOpts.LabelSelector.String())
	})
}
