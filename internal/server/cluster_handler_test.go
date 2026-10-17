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
	"github.com/divergedev/diverge/api/v1alpha1"
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

func TestClusterService_GetClusterInfo_RequestedAndFallbackNamespaceQuota(t *testing.T) {
	c, k8s, logger, audit := setupClusterAuthTest(t)
	k8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = true
		return true, sar, nil
	})

	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "alice"})

	// Create an environment in "staging" namespace while "default" has no quota annotations
	err := c.Create(ctx, &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-staging",
			Namespace: "staging",
		},
	})
	require.NoError(t, err)

	_, err = k8s.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "staging",
			Annotations: map[string]string{
				"divergedev.com/max-active-environments": "4",
				"divergedev.com/max-cnpg-clusters":       "1",
				"divergedev.com/max-hourly-cost-usd":     "0.50",
				"divergedev.com/quota-exceed-policy":     "reject",
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	svc := NewClusterService(c, k8s, logger, audit, "v0.18.0")

	// 1. Explicit namespace request
	resExplicit, err := svc.GetClusterInfo(ctx, connect.NewRequest(&pb.GetClusterInfoRequest{Namespace: "staging"}))
	require.NoError(t, err)
	assert.Equal(t, []string{"staging"}, resExplicit.Msg.Namespaces)
	require.NotNil(t, resExplicit.Msg.NamespaceQuota)
	assert.Equal(t, int32(4), resExplicit.Msg.NamespaceQuota.MaxActiveEnvironments)
	assert.Equal(t, int32(1), resExplicit.Msg.NamespaceQuota.MaxCnpgClusters)
	assert.InDelta(t, 0.50, resExplicit.Msg.NamespaceQuota.MaxHourlyCostUsd, 1e-6)
	assert.Equal(t, "reject", resExplicit.Msg.NamespaceQuota.QuotaExceedPolicy)

	// 2. Fallback discovery when "default" has no quota annotations
	resFallback, err := svc.GetClusterInfo(ctx, connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.NoError(t, err)
	assert.Equal(t, []string{"staging"}, resFallback.Msg.Namespaces)
	require.NotNil(t, resFallback.Msg.NamespaceQuota)
	assert.Equal(t, int32(4), resFallback.Msg.NamespaceQuota.MaxActiveEnvironments)
	assert.Equal(t, int32(1), resFallback.Msg.NamespaceQuota.MaxCnpgClusters)
	assert.InDelta(t, 0.50, resFallback.Msg.NamespaceQuota.MaxHourlyCostUsd, 1e-6)
	assert.Equal(t, "reject", resFallback.Msg.NamespaceQuota.QuotaExceedPolicy)
}
