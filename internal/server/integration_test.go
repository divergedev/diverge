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
	"sync/atomic"
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
	pkgauth "github.com/divergedev/diverge/pkg/auth"
)

var (
	testEnv          *envtest.Environment
	k8sClient        kubernetes.Interface
	ctrlClient       client.Client
	httpServer       *httptest.Server
	testMux          *http.ServeMux
	macaroonProvider *pkgauth.MacaroonProvider
	testEnvToken     string
	readOnlyToken    string
	noPermsToken     string
	nsAToken         string
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
				APIGroups: []string{"divergedev.com"},
				Resources: []string{"environments", "previewgroups", "agenttasks"},
				Verbs:     []string{"*"},
			},
		},
	}, metav1.CreateOptions{})

	_, _ = k8sClient.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "diverge-reader"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"divergedev.com"},
				Resources: []string{"environments", "previewgroups", "agenttasks"},
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
	macaroonProvider = pkgauth.NewMacaroonProvider([]byte("0123456789abcdef0123456789abcdef"))
	provider := auth.NewCompositeProvider(slog.Default())
	provider.Add("macaroon", auth.NewMacaroonAuthProvider(macaroonProvider, nil).WithTaskActiveCheck(ctrlClient))
	provider.Add("tokenreview", auth.NewTokenReviewProvider(k8sClient, nil))
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

func getTaskClient(token string) divergev1alpha1connect.AgentTaskServiceClient {
	return divergev1alpha1connect.NewAgentTaskServiceClient(
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

var nsSeq uint64

func uniqueNamespace(t *testing.T) string {
	t.Helper()
	seq := atomic.AddUint64(&nsSeq, 1)
	clean := strings.ToLower(t.Name())
	clean = strings.ReplaceAll(clean, "_", "-")
	clean = strings.ReplaceAll(clean, "/", "-")
	if len(clean) > 40 {
		clean = clean[:40]
	}
	clean = strings.Trim(clean, "-")
	return fmt.Sprintf("t-%s-%d", clean, seq)
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
	// testEnvToken has a ClusterRoleBinding to diverge-admin
	adminClient := getClusterClient(testEnvToken)
	resp, err := adminClient.GetClusterInfo(context.Background(), connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.NoError(t, err)
	assert.NotNil(t, resp.Msg)

	// noPerms has neither
	_, noPermsTok := setupTestNamespace(t, "noperms", "diverge-none")
	noPermsClient := getClusterClient(noPermsTok)
	_, err = noPermsClient.GetClusterInfo(context.Background(), connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestInputValidation(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	_, token := setupTestNamespace(t, "admin", "diverge-admin")
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
	assert.NotContains(t, err.Error(), "environments.divergedev.com") // Should be sanitized
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
			Spec: &pb.EnvironmentSpec{
				Source: &pb.EnvironmentSource{
					Provider: "github",
					Project:  "divergedev/diverge",
				},
			},
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
			Spec: &pb.EnvironmentSpec{
				Source: &pb.EnvironmentSource{
					Provider: "github",
					Project:  "divergedev/diverge",
				},
			},
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
			Spec: &pb.EnvironmentSpec{
				Source: &pb.EnvironmentSource{
					Provider: "github",
					Project:  "divergedev/diverge",
				},
			},
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
	_, token := setupTestNamespace(t, "admin", "diverge-admin")
	client := getEnvClient(token)

	rapid.Check(t, func(rt *rapid.T) {
		ns := rapid.String().Draw(rt, "namespace")

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
	_, token := setupTestNamespace(t, "admin", "diverge-admin")
	client := getEnvClient(token)

	cases := []struct {
		name  string
		ns    string
		valid bool
	}{
		{"leading-hyphen", "-invalid", false},
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

func TestMacaroonCapabilityToken_Integration(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ctx := context.Background()

	nsA, adminTokenA := setupTestNamespace(t, "admin-a", "diverge-admin")
	nsB, _ := setupTestNamespace(t, "admin-b", "diverge-admin")

	adminTaskClient := getTaskClient(adminTokenA)
	_, err := adminTaskClient.CreateTask(ctx, connect.NewRequest(&pb.CreateTaskRequest{
		Namespace: nsA,
		Name:      "task-alpha",
		Spec: &pb.AgentTaskSpec{
			Objective: "Fix login redirect",
			Repository: &pb.AgentTaskRepository{
				Url: "https://github.com/divergedev/diverge",
			},
		},
	}))
	require.NoError(t, err)

	_, err = adminTaskClient.CreateTask(ctx, connect.NewRequest(&pb.CreateTaskRequest{
		Namespace: nsA,
		Name:      "task-beta",
		Spec: &pb.AgentTaskSpec{
			Objective: "Add rate limiter",
			Repository: &pb.AgentTaskRepository{
				Url: "https://github.com/divergedev/diverge",
			},
		},
	}))
	require.NoError(t, err)

	capToken, err := macaroonProvider.Mint(ctx, pkgauth.Claims{
		TaskID:    "task-alpha",
		Namespace: nsA,
		AllowedTools: []string{
			"diverge_list_*",
			"diverge_get_*",
			"diverge_status",
			"diverge_task_status",
			"diverge_task_logs",
			"diverge_up",
			"diverge_down",
		},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	rawToken, err := capToken.Serialize()
	require.NoError(t, err)

	macaroonTaskClient := getTaskClient(string(rawToken))
	macaroonEnvClient := getEnvClient(string(rawToken))

	// 1. Reading own task succeeds via Macaroon capability token (no K8s TokenReview/SAR)
	ownTaskResp, err := macaroonTaskClient.GetTask(ctx, connect.NewRequest(&pb.GetTaskRequest{
		Namespace: nsA,
		Name:      "task-alpha",
	}))
	require.NoError(t, err)
	assert.Equal(t, "task-alpha", ownTaskResp.Msg.Task.Name)

	// 2. Reading sibling task in same namespace is denied
	_, err = macaroonTaskClient.GetTask(ctx, connect.NewRequest(&pb.GetTaskRequest{
		Namespace: nsA,
		Name:      "task-beta",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// 3. Listing or mutating tasks via Macaroon token is denied
	_, err = macaroonTaskClient.ListTasks(ctx, connect.NewRequest(&pb.ListTasksRequest{
		Namespace: nsA,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, err = macaroonTaskClient.DeleteTask(ctx, connect.NewRequest(&pb.DeleteTaskRequest{
		Namespace: nsA,
		Name:      "task-alpha",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// 4. Listing environments in own namespace succeeds, cross-namespace is denied
	_, err = macaroonEnvClient.ListEnvironments(ctx, connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: nsA,
	}))
	require.NoError(t, err)

	_, err = macaroonEnvClient.ListEnvironments(ctx, connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: nsB,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}
