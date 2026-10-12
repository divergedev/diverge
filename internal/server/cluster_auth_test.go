package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server/auth"
)

func setupClusterAuthTest(t *testing.T) (client.Client, *fakek8s.Clientset, *slog.Logger, *AuditLogger) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	k8s := fakek8s.NewSimpleClientset()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	audit := NewAuditLogger(logger)
	return c, k8s, logger, audit
}

func TestClusterService_GetClusterInfo(t *testing.T) {
	c, k8s, logger, audit := setupClusterAuthTest(t)
	k8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = true
		return true, sar, nil
	})

	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "alice"})

	// Seed 2 environments and 1 preview group
	require.NoError(t, c.Create(ctx, &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "env-1", Namespace: "default"},
	}))
	require.NoError(t, c.Create(ctx, &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "env-2", Namespace: "staging"},
	}))
	require.NoError(t, c.Create(ctx, &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-1", Namespace: "default"},
	}))

	// Default version fallback when empty
	svcDefault := NewClusterService(c, k8s, logger, audit, "")
	resDefault, err := svcDefault.GetClusterInfo(ctx, connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "dev", resDefault.Msg.ControllerVersion)
	assert.True(t, resDefault.Msg.ControllerHealthy)
	assert.Equal(t, int32(2), resDefault.Msg.EnvironmentCount)
	assert.Equal(t, int32(1), resDefault.Msg.PreviewGroupCount)

	// Explicit version
	svcVersioned := NewClusterService(c, k8s, logger, audit, "v0.9.2")
	resVersioned, err := svcVersioned.GetClusterInfo(ctx, connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "v0.9.2", resVersioned.Msg.ControllerVersion)
}

func TestClusterService_GetClusterInfo_RBACDenied(t *testing.T) {
	c, k8s, logger, audit := setupClusterAuthTest(t)
	k8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = false
		return true, sar, nil
	})

	svc := NewClusterService(c, k8s, logger, audit, "v1.0.0")
	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "bob"})
	_, err := svc.GetClusterInfo(ctx, connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestAuthService_GetCurrentUser(t *testing.T) {
	_, k8s, logger, audit := setupClusterAuthTest(t)
	svc := NewAuthService(k8s, logger, audit)

	// 1. Unauthenticated -> CodeUnauthenticated
	_, err := svc.GetCurrentUser(context.Background(), connect.NewRequest(&pb.GetCurrentUserRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// 2. Kubernetes SA user (no email) -> Issuer: "kubernetes"
	k8sCtx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{
		Username: "system:serviceaccount:default:deployer",
		Groups:   []string{"system:serviceaccounts", "system:authenticated"},
	})
	k8sRes, err := svc.GetCurrentUser(k8sCtx, connect.NewRequest(&pb.GetCurrentUserRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "system:serviceaccount:default:deployer", k8sRes.Msg.UserId)
	assert.Equal(t, "kubernetes", k8sRes.Msg.Issuer)
	assert.Empty(t, k8sRes.Msg.Email)

	// 3. OIDC user (with email) -> Issuer: "oidc"
	oidcCtx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{
		Username: "alice",
		Email:    "alice@example.com",
		Groups:   []string{"platform-admins"},
	})
	oidcRes, err := svc.GetCurrentUser(oidcCtx, connect.NewRequest(&pb.GetCurrentUserRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "alice", oidcRes.Msg.UserId)
	assert.Equal(t, "alice@example.com", oidcRes.Msg.Email)
	assert.Equal(t, "oidc", oidcRes.Msg.Issuer)
	assert.Equal(t, []string{"platform-admins"}, oidcRes.Msg.Groups)
}

func TestAuthService_ListPermissions(t *testing.T) {
	_, k8s, logger, audit := setupClusterAuthTest(t)
	k8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		attrs := sar.Spec.ResourceAttributes
		// Allow only "get" and "list" on "environments", and "get" on "agenttasks"
		if attrs.Resource == "environments" && (attrs.Verb == "get" || attrs.Verb == "list") {
			sar.Status.Allowed = true
		} else if attrs.Resource == "agenttasks" && attrs.Verb == "get" {
			sar.Status.Allowed = true
		} else {
			sar.Status.Allowed = false
		}
		return true, sar, nil
	})

	svc := NewAuthService(k8s, logger, audit)
	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "dev-user"})

	// 1. Unauthenticated -> CodeUnauthenticated
	_, err := svc.ListPermissions(context.Background(), connect.NewRequest(&pb.ListPermissionsRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// 2. Invalid namespace -> CodeInvalidArgument
	_, err = svc.ListPermissions(ctx, connect.NewRequest(&pb.ListPermissionsRequest{Namespace: "INVALID_NS"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// 3. Valid permission scan with default namespace fallback
	res, err := svc.ListPermissions(ctx, connect.NewRequest(&pb.ListPermissionsRequest{}))
	require.NoError(t, err)
	require.Len(t, res.Msg.Permissions, 2)
	assert.Equal(t, "environments", res.Msg.Permissions[0].Resource)
	assert.Equal(t, []string{"get", "list"}, res.Msg.Permissions[0].Verbs)
	assert.Equal(t, []string{"default"}, res.Msg.Permissions[0].Namespaces)
	assert.Equal(t, "agenttasks", res.Msg.Permissions[1].Resource)
	assert.Equal(t, []string{"get"}, res.Msg.Permissions[1].Verbs)

	// 4. Underlying SAR API failure -> CodeInternal
	k8sErr := fakek8s.NewSimpleClientset()
	k8sErr.PrependReactor("create", "subjectaccessreviews", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("apiserver unavailable")
	})
	svcErr := NewAuthService(k8sErr, logger, audit)
	_, err = svcErr.ListPermissions(ctx, connect.NewRequest(&pb.ListPermissionsRequest{Namespace: "default"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}
