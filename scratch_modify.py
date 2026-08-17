import re

with open("internal/server/integration_test.go", "r") as f:
    content = f.read()

# 1. Add imports: fmt, strings, rapid
content = content.replace(
    '"context"',
    '"context"\n\t"fmt"\n\t"strings"\n\t"pgregory.net/rapid"'
)

# 2. C2: silent pass TestMain
content = content.replace(
    'if os.Getenv("KUBEBUILDER_ASSETS") == "" {\n\t\t// Just skip early if no envtest binaries\n\t\tos.Exit(0)\n\t}',
    'if os.Getenv("KUBEBUILDER_ASSETS") == "" {\n\t\tfmt.Println("SKIP: KUBEBUILDER_ASSETS not set, skipping integration tests")\n\t\tos.Exit(0)\n\t}'
)

# 3. C1: os.Exit(m.Run())
content = content.replace('defer testEnv.Stop()\n', '')
content = content.replace('defer httpServer.Close()\n', '')
content = content.replace(
    'os.Exit(m.Run())',
    '''code := m.Run()
	if httpServer != nil {
		httpServer.Close()
	}
	if testEnv != nil {
		if err := testEnv.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to stop testEnv: %v\\n", err)
		}
	}
	os.Exit(code)'''
)

# 4. skipIfNoEnvtest helper and uniqueNamespace
helpers = '''
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

'''

content = content.replace('func TestAuth_ValidToken(t *testing.T) {', helpers + 'func TestAuth_ValidToken(t *testing.T) {')

# 5. Replace skips in existing tests
skip_pattern = re.compile(r'if os\.Getenv\("KUBEBUILDER_ASSETS"\) == "" \{\n\t\tt\.Skip\("envtest binaries not available, set KUBEBUILDER_ASSETS"\)\n\t\}')
content = skip_pattern.sub('skipIfNoEnvtest(t)', content)

# 6. M4: Add t.Parallel() to existing read-only tests, M2: Payload assertions, H3: use isolated namespaces
# TestAuth_ValidToken
content = content.replace(
    '''func TestAuth_ValidToken(t *testing.T) {
	skipIfNoEnvtest(t)
	client := getEnvClient(testEnvToken)
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "test-ns",
	}))
	require.NoError(t, err)
}''',
    '''func TestAuth_ValidToken(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns, token := setupTestNamespace(t, "admin", "diverge-admin")
	client := getEnvClient(token)
	resp, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: ns,
	}))
	require.NoError(t, err)
	assert.NotNil(t, resp.Msg)
}'''
)

# TestAuth_InvalidToken
content = content.replace(
    '''func TestAuth_InvalidToken(t *testing.T) {
	skipIfNoEnvtest(t)
	client := getEnvClient("invalid-token")
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "test-ns",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}''',
    '''func TestAuth_InvalidToken(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns := uniqueNamespace(t)
	client := getEnvClient("invalid-token")
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: ns,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}'''
)

# TestAuth_NoToken
content = content.replace(
    '''func TestAuth_NoToken(t *testing.T) {
	skipIfNoEnvtest(t)
	client := getEnvClient("")
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "test-ns",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}''',
    '''func TestAuth_NoToken(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns := uniqueNamespace(t)
	client := getEnvClient("")
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: ns,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}'''
)

# TestRBAC_ReadOnly
content = content.replace(
    '''func TestRBAC_ReadOnly(t *testing.T) {
	skipIfNoEnvtest(t)
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
}''',
    '''func TestRBAC_ReadOnly(t *testing.T) {
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
}'''
)

# TestRBAC_NoPerms
content = content.replace(
    '''func TestRBAC_NoPerms(t *testing.T) {
	skipIfNoEnvtest(t)
	client := getEnvClient(noPermsToken)

	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "test-ns",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}''',
    '''func TestRBAC_NoPerms(t *testing.T) {
	t.Parallel()
	skipIfNoEnvtest(t)
	ns, token := setupTestNamespace(t, "noperms", "diverge-none") // assuming diverge-none doesn't exist, meaning no perms
	client := getEnvClient(token)

	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: ns,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}'''
)

# TestNamespaceIsolation
content = content.replace(
    '''func TestNamespaceIsolation(t *testing.T) {
	skipIfNoEnvtest(t)
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
}''',
    '''func TestNamespaceIsolation(t *testing.T) {
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
}'''
)

# TestGetClusterInfo_RequiresBothResources
content = content.replace(
    '''func TestGetClusterInfo_RequiresBothResources(t *testing.T) {
	skipIfNoEnvtest(t)
	// admin has both
	adminClient := getClusterClient(testEnvToken)
	_, err := adminClient.GetClusterInfo(context.Background(), connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.NoError(t, err)

	// noPerms has neither
	noPermsClient := getClusterClient(noPermsToken)
	_, err = noPermsClient.GetClusterInfo(context.Background(), connect.NewRequest(&pb.GetClusterInfoRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}''',
    '''func TestGetClusterInfo_RequiresBothResources(t *testing.T) {
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
}'''
)

# TestInputValidation
content = content.replace(
    '''func TestInputValidation(t *testing.T) {
	skipIfNoEnvtest(t)
	client := getEnvClient(testEnvToken)

	// Invalid namespace
	_, err := client.ListEnvironments(context.Background(), connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: "INVALID_NS!",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}''',
    '''func TestInputValidation(t *testing.T) {
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
}'''
)

# TestErrorSanitization
content = content.replace(
    '''func TestErrorSanitization(t *testing.T) {
	skipIfNoEnvtest(t)
	client := getEnvClient(testEnvToken)

	// Try to get a non-existent resource
	_, err := client.GetEnvironment(context.Background(), connect.NewRequest(&pb.GetEnvironmentRequest{
		Namespace: "test-ns",
		Name:      "does-not-exist",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	assert.NotContains(t, err.Error(), "environments.diverge.dev") // Should be sanitized
}''',
    '''func TestErrorSanitization(t *testing.T) {
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
}'''
)

# 7. Add new tests: H1 (mutation), M1 (PBT), M3 (Boundary)
new_tests = '''
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

	cases := []struct{
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
'''
content = content + "\n" + new_tests

with open("internal/server/integration_test.go", "w") as f:
    f.write(content)
