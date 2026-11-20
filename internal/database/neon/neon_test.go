package neon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
	pkgdb "github.com/divergedev/diverge/pkg/database"
)

func TestBranchName_DeterministicAndCollisionFreeAcrossNamespaces(t *testing.T) {
	a := BranchName("team-a", "pr-42")
	b := BranchName("team-b", "pr-42")
	assert.NotEqual(t, a, b, "same env name in different namespaces must produce distinct Neon branch names")
	assert.Equal(t, a, BranchName("team-a", "pr-42"))
	assert.True(t, strings.HasPrefix(a, "diverge-pr-42-"))
	assert.LessOrEqual(t, len(a), 63)

	longName := strings.Repeat("feature-very-long-branch-name-", 4)
	longBranch := BranchName("production-preview-ns", longName)
	assert.LessOrEqual(t, len(longBranch), 63)
	assert.False(t, strings.HasSuffix(longBranch, "-"))
}

type fakeNeonServer struct {
	mu          sync.Mutex
	branches    map[string]neonBranch
	createCalls int
	deleteCalls int
	lockedNext  bool
	expectToken string
}

func newFakeNeonServer(t *testing.T, token string) (*fakeNeonServer, *httptest.Server) {
	t.Helper()
	state := &fakeNeonServer{
		branches:    make(map[string]neonBranch),
		expectToken: token,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+state.expectToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"UNAUTHORIZED","message":"invalid api key"}`))
			return
		}

		state.mu.Lock()
		defer state.mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/proj-123/branches":
			items := make([]neonBranch, 0, len(state.branches))
			for _, br := range state.branches {
				items = append(items, br)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(neonListBranchesResponse{Branches: items})

		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/projects/proj-123/branches":
			state.createCalls++
			if state.lockedNext {
				state.lockedNext = false
				w.WriteHeader(http.StatusLocked)
				_, _ = w.Write([]byte(`{"code":"PROJECT_LOCKED","message":"ongoing operation"}`))
				return
			}
			var req neonCreateBranchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			for _, existing := range state.branches {
				if existing.Name == req.Branch.Name {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"code":"BRANCH_EXISTS","message":"branch already exists"}`))
					return
				}
			}
			created := neonBranch{
				ID:           "br-ephemeral-42",
				Name:         req.Branch.Name,
				ParentID:     req.Branch.ParentID,
				CurrentState: "ready",
			}
			state.branches[created.ID] = created
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(neonCreateBranchResponse{Branch: created})

		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/proj-123/connection_uri":
			branchID := r.URL.Query().Get("branch_id")
			dbName := r.URL.Query().Get("database_name")
			role := r.URL.Query().Get("role_name")
			pooled := r.URL.Query().Get("pooled") == "true"
			host := "ep-42.us-east-2.aws.neon.tech"
			if pooled {
				host = "ep-42-pooler.us-east-2.aws.neon.tech"
			}
			uri := "postgresql://" + role + ":s3cr3t@" + host + "/" + dbName + "?sslmode=require&options=branch%3D" + branchID
			_ = json.NewEncoder(w).Encode(neonConnectionURIResponse{URI: uri})

		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v2/projects/proj-123/branches/"):
			state.deleteCalls++
			id := strings.TrimPrefix(r.URL.Path, "/api/v2/projects/proj-123/branches/")
			if _, ok := state.branches[id]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(state.branches, id)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return state, srv
}

func TestNeonDatabaseProvider_ProvisionStatusAndTeardownLifecycle(t *testing.T) {
	state, srv := newFakeNeonServer(t, "neon-secret-key")

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "neon-creds",
			Namespace: "payments",
		},
		Data: map[string][]byte{
			"apiKey":         []byte("neon-secret-key"),
			"projectId":      []byte("proj-123"),
			"parentBranchId": []byte("br-main-prod"),
			"databaseName":   []byte("payments_db"),
			"roleName":       []byte("payments_app"),
			"baseUrl":        []byte(srv.URL + "/api/v2"),
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(credSecret).Build()

	prov := &NeonDatabaseProvider{
		Client:                      k8sClient,
		AllowInsecureHTTPForTesting: true,
		AllowLoopbackForTesting:     true,
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-42",
			Namespace: "payments",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Database: v1alpha1.EnvironmentDatabase{
				Mode:          "neon",
				ConnectionRef: "neon-creds",
			},
		},
	}

	// 1. Project lock (423) yields Ready=false without error so controller requeues cleanly.
	state.mu.Lock()
	state.lockedNext = true
	state.mu.Unlock()
	lockedRes, err := prov.Provision(context.Background(), env)
	require.NoError(t, err)
	assert.False(t, lockedRes.Ready)
	assert.Contains(t, lockedRes.Message, "busy")

	// 2. Subsequent reconcile creates CoW branch from parent br-main-prod and resolves pooled + direct URIs.
	res, err := prov.Provision(context.Background(), env)
	require.NoError(t, err)
	require.True(t, res.Ready)
	assert.Contains(t, res.DSN, "-pooler.us-east-2.aws.neon.tech/payments_db")
	assert.Contains(t, res.AdminDSN, "ep-42.us-east-2.aws.neon.tech/payments_db")
	assert.Equal(t, res.DSN, res.EnvVars["DATABASE_URL"])
	assert.Equal(t, res.AdminDSN, res.EnvVars["DATABASE_URL_DIRECT"])
	assert.Equal(t, "br-ephemeral-42", res.EnvVars["DIVERGE_NEON_BRANCH_ID"])
	assert.Equal(t, BranchName("payments", "pr-42"), res.EnvVars["DIVERGE_NEON_BRANCH_NAME"])
	assert.Equal(t, "proj-123", res.EnvVars["DIVERGE_NEON_PROJECT_ID"])

	state.mu.Lock()
	createdBranch := state.branches["br-ephemeral-42"]
	assert.Equal(t, "br-main-prod", createdBranch.ParentID)
	assert.Equal(t, 2, state.createCalls)
	state.mu.Unlock()

	// 3. Re-reconcile is idempotent (reuses existing branch without calling POST again).
	resAgain, err := prov.Provision(context.Background(), env)
	require.NoError(t, err)
	assert.True(t, resAgain.Ready)
	state.mu.Lock()
	assert.Equal(t, 2, state.createCalls)
	state.mu.Unlock()

	// 4. Status reports Provisioned=true.
	st, err := prov.Status(context.Background(), env)
	require.NoError(t, err)
	assert.True(t, st.Provisioned)
	assert.Equal(t, BranchName("payments", "pr-42"), st.SchemaName)

	// 5. Teardown deletes the branch, and repeated Teardown is idempotent.
	require.NoError(t, prov.Teardown(context.Background(), env))
	require.NoError(t, prov.Teardown(context.Background(), env))

	stAfter, err := prov.Status(context.Background(), env)
	require.NoError(t, err)
	assert.False(t, stAfter.Provisioned)
}

func TestNeonDatabaseProvider_SecurityGuards(t *testing.T) {
	_, srv := newFakeNeonServer(t, "tok")

	t.Run("rejects insecure http baseUrl when testing flag is false", func(t *testing.T) {
		prov := &NeonDatabaseProvider{
			APIKey:                  "tok",
			ProjectID:               "proj-123",
			BaseURL:                 srv.URL + "/api/v2",
			AllowLoopbackForTesting: true,
		}
		env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "pr-1", Namespace: "default"}}
		_, err := prov.Provision(context.Background(), env)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must use https://")
	})

	t.Run("blocks loopback SSRF dial when testing flag is false", func(t *testing.T) {
		tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(tlsSrv.Close)

		prov := &NeonDatabaseProvider{
			APIKey:    "tok",
			ProjectID: "proj-123",
			BaseURL:   tlsSrv.URL + "/api/v2",
		}
		env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "pr-1", Namespace: "default"}}
		_, err := prov.Provision(context.Background(), env)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "blocked non-public IP")
	})

	t.Run("rejects path traversal in projectID and parentBranchID", func(t *testing.T) {
		prov := &NeonDatabaseProvider{
			APIKey:                      "tok",
			ProjectID:                   "../admin",
			BaseURL:                     srv.URL + "/api/v2",
			AllowInsecureHTTPForTesting: true,
			AllowLoopbackForTesting:     true,
		}
		env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "pr-1", Namespace: "default"}}
		_, err := prov.Provision(context.Background(), env)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid Neon project ID")
	})

	t.Run("registered in pkgdb.Providers", func(t *testing.T) {
		assert.True(t, pkgdb.Providers.Has("neon"))
	})
}

func TestBranchName_Invariants_PBT(t *testing.T) {
	validDNS1123 := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	hegel.Test(t, func(ht *hegel.T) {
		ns := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(80))
		envName := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(120))

		b := BranchName(ns, envName)
		require.NotEmpty(ht, b)
		require.LessOrEqual(ht, len(b), 63)
		require.True(ht, validDNS1123.MatchString(b), "branch name %q must conform to DNS-1123", b)
		require.Equal(ht, b, BranchName(ns, envName))
	})
}
