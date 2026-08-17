//go:build integration

package server_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server"
	"github.com/divergedev/diverge/internal/server/auth"
)

var (
	testEnv       *envtest.Environment
	k8sClient     kubernetes.Interface
	ctrlClient    client.Client
	httpServer    *httptest.Server
	testMux       *http.ServeMux
	testEnvToken  string
	readOnlyToken string
	noPermsToken  string
	nsAToken      string
)

func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		// Just skip early if no envtest binaries
		os.Exit(0)
	}

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")},
	}

	cfg, err := testEnv.Start()
	if err != nil {
		panic(err)
	}
	defer testEnv.Stop()

	err = v1alpha1.AddToScheme(scheme.Scheme)
	if err != nil {
		panic(err)
	}

	ctrlClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		panic(err)
	}

	k8sClient, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	// Create namespaces
	for _, ns := range []string{"test-ns", "ns-a", "ns-b"} {
		_, _ = k8sClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		}, metav1.CreateOptions{})
	}

	// Create ServiceAccounts
	createSA := func(name, namespace string) string {
		_, err := k8sClient.CoreV1().ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: name},
		}, metav1.CreateOptions{})
		if err != nil {
			panic(err)
		}

		tr, err := k8sClient.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, name, &authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				ExpirationSeconds: ptr.To[int64](3600),
			},
		}, metav1.CreateOptions{})
		if err != nil {
			panic(err)
		}
		return tr.Status.Token
	}

	testEnvToken = createSA("admin", "test-ns")
	readOnlyToken = createSA("readonly", "test-ns")
	noPermsToken = createSA("noperms", "test-ns")
	nsAToken = createSA("ns-a-user", "ns-a")

	// Setup RBAC
	_, _ = k8sClient.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "diverge-admin"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"diverge.dev"},
				Resources: []string{"environments", "previewgroups"},
				Verbs:     []string{"*"},
			},
		},
	}, metav1.CreateOptions{})

	_, _ = k8sClient.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "diverge-reader"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"diverge.dev"},
				Resources: []string{"environments", "previewgroups"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}, metav1.CreateOptions{})

	// Bind admin
	_, _ = k8sClient.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "admin-binding"},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "diverge-admin"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "admin", Namespace: "test-ns"}},
	}, metav1.CreateOptions{})

	// Bind reader
	_, _ = k8sClient.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "reader-binding"},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "diverge-reader"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "readonly", Namespace: "test-ns"}},
	}, metav1.CreateOptions{})

	// Bind ns-a user (only inside ns-a)
	_, _ = k8sClient.RbacV1().RoleBindings("ns-a").Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "ns-a-binding"},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "diverge-admin"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "ns-a-user", Namespace: "ns-a"}},
	}, metav1.CreateOptions{})

	// Server setup
	provider := auth.NewTokenReviewProvider(k8sClient, nil)
	authMw := auth.NewMiddleware(auth.MiddlewareConfig{
		Provider: provider,
		Cache:    auth.NewTokenCache(100, time.Minute),
		Logger:   slog.Default(),
	})

	muxCfg := server.ServeMuxConfig{
		Client:    ctrlClient,
		K8sClient: k8sClient,
		Version:   "test",
	}
	testMux = server.NewServeMux(muxCfg)

	httpServer = httptest.NewServer(authMw(testMux))
	defer httpServer.Close()

	os.Exit(m.Run())
}

func getEnvClient(token string) divergev1alpha1connect.EnvironmentServiceClient {
	return divergev1alpha1connect.NewEnvironmentServiceClient(
		httpServer.Client(),
		httpServer.URL,
		connect.WithInterceptors(authInterceptor(token)),
	)
}

func getClusterClient(token string) divergev1alpha1connect.ClusterServiceClient {
	return divergev1alpha1connect.NewClusterServiceClient(
		httpServer.Client(),
		httpServer.URL,
		connect.WithInterceptors(authInterceptor(token)),
	)
}

func authInterceptor(token string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if token != "" {
				req.Header().Set("Authorization", "Bearer "+token)
			}
			return next(ctx, req)
		}
	}
}

func TestAuth_ValidToken(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("envtest binaries not available, set KUBEBUILDER_ASSETS")
	}
	client := getEnvClient(testEnvToken)
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "test-ns",
	}))
	require.NoError(t, err)
}

func TestAuth_InvalidToken(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("envtest binaries not available, set KUBEBUILDER_ASSETS")
	}
	client := getEnvClient("invalid-token")
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "test-ns",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestAuth_NoToken(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("envtest binaries not available, set KUBEBUILDER_ASSETS")
	}
	client := getEnvClient("")
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "test-ns",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestRBAC_ReadOnly(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("envtest binaries not available, set KUBEBUILDER_ASSETS")
	}
	client := getEnvClient(readOnlyToken)

	// List should succeed
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "test-ns",
	}))
	require.NoError(t, err)

	// Create should fail
	_, err = client.CreateEnvironment(context.Background(), connect.NewRequest(&pb.CreateEnvironmentRequest{
		Namespace: "test-ns",
		Environment: &pb.Environment{
			Name: "test-env",
		},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestRBAC_NoPerms(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("envtest binaries not available, set KUBEBUILDER_ASSETS")
	}
	client := getEnvClient(noPermsToken)

	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "test-ns",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestNamespaceIsolation(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("envtest binaries not available, set KUBEBUILDER_ASSETS")
	}
	client := getEnvClient(nsAToken)

	// Access to ns-a should succeed
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "ns-a",
	}))
	require.NoError(t, err)

	// Access to ns-b should fail
	_, err = client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "ns-b",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestGetClusterInfo_RequiresBothResources(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("envtest binaries not available, set KUBEBUILDER_ASSETS")
	}
	// admin has both
	adminClient := getClusterClient(testEnvToken)
	_, err := adminClient.GetClusterInfo(context.Background(), connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.NoError(t, err)

	// noPerms has neither
	noPermsClient := getClusterClient(noPermsToken)
	_, err = noPermsClient.GetClusterInfo(context.Background(), connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestInputValidation(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("envtest binaries not available, set KUBEBUILDER_ASSETS")
	}
	client := getEnvClient(testEnvToken)

	// Invalid namespace
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "INVALID_NS!",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestErrorSanitization(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("envtest binaries not available, set KUBEBUILDER_ASSETS")
	}
	client := getEnvClient(testEnvToken)

	// Try to get a non-existent resource
	_, err := client.GetEnvironment(context.Background(), connect.NewRequest(&pb.GetEnvironmentRequest{
		Namespace: "test-ns",
		Name:      "does-not-exist",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	assert.NotContains(t, err.Error(), "environments.diverge.dev") // Should be sanitized
}
