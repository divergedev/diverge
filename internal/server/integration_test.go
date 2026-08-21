//go:build integration

package server_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"pgregory.net/rapid"
	"strings"
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
		fmt.Println("SKIP: KUBEBUILDER_ASSETS not set, skipping integration tests")
		os.Exit(0)
	}

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")},
	}

	cfg, err := testEnv.Start()
	if err != nil {
		panic(err)
	}

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
	testMux, _ = server.NewServeMux(muxCfg)

	httpServer = httptest.NewServer(authMw(testMux))

	code := m.Run()
	if httpServer != nil {
		httpServer.Close()
	}
	if testEnv != nil {
		if err := testEnv.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to stop testEnv: %v\n", err)
		}
	}
	os.Exit(code)
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

func skipIfNoEnvtest(t *testing.T) {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set")
	}
}

func uniqueNamespace(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("test-%s", strings.ToLower(t.Name()))
}

func setupTestNamespace(t *testing.T, saName, roleName string) (string, string) {
	t.Helper()
	ns := uniqueNamespace(t)
	ctx := context.Background()

	_, err := k8sClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = k8sClient.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{})
	})

	_, err = k8sClient.CoreV1().ServiceAccounts(ns).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: saName},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	tr, err := k8sClient.CoreV1().ServiceAccounts(ns).CreateToken(ctx, saName, &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: ptr.To[int64](3600),
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = k8sClient.RbacV1().RoleBindings(ns).Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: saName + "-binding"},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: roleName},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: saName, Namespace: ns}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	return ns, tr.Status.Token
}

func TestAuth_ValidToken(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns, token := setupTestNamespace(t, "admin", "diverge-admin")
	client := getEnvClient(token)
	resp, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: ns,
	}))
	require.NoError(t, err)
	assert.NotNil(t, resp.Msg)
}

func TestAuth_InvalidToken(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns := uniqueNamespace(t)
	client := getEnvClient("invalid-token")
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: ns,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestAuth_NoToken(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns := uniqueNamespace(t)
	client := getEnvClient("")
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: ns,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestRBAC_ReadOnly(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns, token := setupTestNamespace(t, "readonly", "diverge-reader")
	client := getEnvClient(token)

	// List should succeed
	resp, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: ns,
	}))
	require.NoError(t, err)
	assert.NotNil(t, resp.Msg)

	// Create should fail
	_, err = client.CreateEnvironment(context.Background(), connect.NewRequest(&pb.CreateEnvironmentRequest{
		Namespace: ns,
		Environment: &pb.Environment{
			Name: "test-env",
		},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestRBAC_NoPerms(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns, token := setupTestNamespace(t, "noperms", "diverge-none") // assuming diverge-none doesn't exist, meaning no perms
	client := getEnvClient(token)

	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: ns,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestNamespaceIsolation(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	nsA, tokenA := setupTestNamespace(t, "ns-a-user", "diverge-admin")
	nsB, _ := setupTestNamespace(t, "ns-b-user", "diverge-admin")
	client := getEnvClient(tokenA)

	// Access to ns-a should succeed
	resp, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: nsA,
	}))
	require.NoError(t, err)
	assert.NotNil(t, resp.Msg)

	// Access to ns-b should fail
	_, err = client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: nsB,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestGetClusterInfo_RequiresBothResources(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	// admin has both
	_, adminToken := setupTestNamespace(t, "admin", "diverge-admin")
	adminClient := getClusterClient(adminToken)
	resp, err := adminClient.GetClusterInfo(context.Background(), connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.NoError(t, err)
	assert.NotNil(t, resp.Msg)

	// noPerms has neither
	_, noPermsToken := setupTestNamespace(t, "noperms", "diverge-none")
	noPermsClient := getClusterClient(noPermsToken)
	_, err = noPermsClient.GetClusterInfo(context.Background(), connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestInputValidation(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns, token := setupTestNamespace(t, "admin", "diverge-admin")
	client := getEnvClient(token)

	// Invalid namespace
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "INVALID_NS!",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestErrorSanitization(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns, token := setupTestNamespace(t, "admin", "diverge-admin")
	client := getEnvClient(token)

	// Try to get a non-existent resource
	_, err := client.GetEnvironment(context.Background(), connect.NewRequest(&pb.GetEnvironmentRequest{
		Namespace: ns,
		Name:      "does-not-exist",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	assert.NotContains(t, err.Error(), "environments.diverge.dev") // Should be sanitized
}

func TestCreateEnvironment_Authorized(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns, token := setupTestNamespace(t, "admin", "diverge-admin")
	client := getEnvClient(token)

	resp, err := client.CreateEnvironment(context.Background(), connect.NewRequest(&pb.CreateEnvironmentRequest{
		Namespace: ns,
		Environment: &pb.Environment{
			Name: "test-env-auth",
		},
	}))
	require.NoError(t, err)
	assert.NotNil(t, resp.Msg)
	assert.Equal(t, "test-env-auth", resp.Msg.Environment.Name)
}

func TestCreateEnvironment_Denied(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns, token := setupTestNamespace(t, "readonly", "diverge-reader")
	client := getEnvClient(token)

	_, err := client.CreateEnvironment(context.Background(), connect.NewRequest(&pb.CreateEnvironmentRequest{
		Namespace: ns,
		Environment: &pb.Environment{
			Name: "test-env-denied",
		},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestDeleteEnvironment_Authorized(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns, token := setupTestNamespace(t, "admin", "diverge-admin")
	client := getEnvClient(token)

	_, err := client.CreateEnvironment(context.Background(), connect.NewRequest(&pb.CreateEnvironmentRequest{
		Namespace: ns,
		Environment: &pb.Environment{
			Name: "test-env-del",
		},
	}))
	require.NoError(t, err)

	_, err = client.DeleteEnvironment(context.Background(), connect.NewRequest(&pb.DeleteEnvironmentRequest{
		Namespace: ns,
		Name:      "test-env-del",
	}))
	require.NoError(t, err)
}

func TestDeleteEnvironment_NotFound(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns, token := setupTestNamespace(t, "admin", "diverge-admin")
	client := getEnvClient(token)

	_, err := client.DeleteEnvironment(context.Background(), connect.NewRequest(&pb.DeleteEnvironmentRequest{
		Namespace: ns,
		Name:      "not-found",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestInputValidation_PBT(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	rapid.Check(t, func(rt *rapid.T) {
		ns := rapid.String().Draw(rt, "namespace")

		_, token := setupTestNamespace(t, "admin", "diverge-admin")
		client := getEnvClient(token)

		// Use the randomized namespace for testing validation
		_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
			Namespace: ns,
		}))

		if err != nil {
			code := connect.CodeOf(err)
			// Must never be Internal or Unknown
			assert.NotEqual(t, connect.CodeInternal, code)
			assert.NotEqual(t, connect.CodeUnknown, code)
		}
	})
}

func TestBoundaryValidation(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns, token := setupTestNamespace(t, "admin", "diverge-admin")
	client := getEnvClient(token)

	cases := []struct {
		name  string
		ns    string
		valid bool
	}{
		{"empty", "", false},
		{"63-char", strings.Repeat("a", 63), true},
		{"64-char", strings.Repeat("a", 64), false},
		{"unicode", "namespace-🌟", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
				Namespace: tc.ns,
			}))
			if tc.valid {
				// If valid namespace but not exists or no perms, shouldn't be InvalidArgument
				if err != nil {
					assert.NotEqual(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				}
			} else {
				require.Error(t, err)
				assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
			}
		})
	}
}
