package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/internal/server/auth"
)

func TestClusterService_GetClusterInfo_NamespaceQuota(t *testing.T) {
	c, k8s, logger, audit := setupClusterAuthTest(t)
	k8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = true
		return true, sar, nil
	})

	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "alice"})

	_, err := k8s.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
			Annotations: map[string]string{
				"divergedev.com/max-active-environments": "5",
				"divergedev.com/max-cnpg-clusters":       "2",
				"divergedev.com/max-hourly-cost-usd":     "$0.25",
				"divergedev.com/quota-exceed-policy":     "reject",
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	svc := NewClusterService(c, k8s, logger, audit, "v0.18.0")
	res, err := svc.GetClusterInfo(ctx, connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.NoError(t, err)
	require.NotNil(t, res.Msg.NamespaceQuota)
	assert.Equal(t, int32(5), res.Msg.NamespaceQuota.MaxActiveEnvironments)
	assert.Equal(t, int32(2), res.Msg.NamespaceQuota.MaxCnpgClusters)
	assert.InDelta(t, 0.25, res.Msg.NamespaceQuota.MaxHourlyCostUsd, 1e-6)
	assert.Equal(t, "reject", res.Msg.NamespaceQuota.QuotaExceedPolicy)
}

func TestClusterService_GetClusterInfo_NamespaceQuotaDefaultPolicy(t *testing.T) {
	c, k8s, logger, audit := setupClusterAuthTest(t)
	k8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = true
		return true, sar, nil
	})

	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "alice"})

	_, err := k8s.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
			Annotations: map[string]string{
				"divergedev.com/max-active-environments": "3",
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	svc := NewClusterService(c, k8s, logger, audit, "v0.18.0")
	res, err := svc.GetClusterInfo(ctx, connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.NoError(t, err)
	require.NotNil(t, res.Msg.NamespaceQuota)
	assert.Equal(t, int32(3), res.Msg.NamespaceQuota.MaxActiveEnvironments)
	assert.Equal(t, "hibernate-oldest", res.Msg.NamespaceQuota.QuotaExceedPolicy)
}
