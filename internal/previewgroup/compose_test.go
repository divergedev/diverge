package previewgroup

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func newTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestNormalizeGroupName(t *testing.T) {
	name, err := NormalizeGroupName("feat/Checkout_V2")
	require.NoError(t, err)
	assert.Equal(t, "feat-checkout-v2", name)

	longBranch := "feat/this-is-an-extremely-long-multi-repository-feature-branch-name-exceeding-sixty-three-chars"
	trunc, err := NormalizeGroupName(longBranch)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(trunc), 63)

	_, err = NormalizeGroupName("")
	require.Error(t, err)

	_, err = NormalizeGroupName("../escape")
	require.Error(t, err)
}

func TestMultiMRJoinSyncAndDetachLifecycle(t *testing.T) {
	ctx := context.Background()

	frontendEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-acme-web-app-101",
			Namespace: "frontend",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "acme/web-app",
				MR:       101,
				Branch:   "feat/checkout",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "web-app",
				Namespace:   "frontend",
				Image:       "ghcr.io/acme/web-app:sha-111",
				Port:        3000,
			},
		},
	}

	backendEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-acme-payments-api-42",
			Namespace: "backend",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "acme/payments-api",
				MR:       42,
				Branch:   "feat/checkout",
			},
			Database: v1alpha1.EnvironmentDatabase{
				Mode:       "neon",
				SeedSource: "main",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "payments-api",
				Namespace:   "backend",
				Image:       "ghcr.io/acme/payments-api:sha-aaa",
				Port:        8080,
			},
		},
	}

	c := newTestClient(t, frontendEnv, backendEnv)

	// 1. Frontend MR #101 joins "feat/checkout" -> creates PreviewGroup "feat-checkout"
	feSvc, feMember, err := ServiceFromEnvironment(frontendEnv)
	require.NoError(t, err)
	ttl := 8 * time.Hour
	res1, err := Join(ctx, c, JoinOptions{
		GroupName: "feat/checkout",
		Services:  []v1alpha1.PreviewGroupServiceSpec{feSvc},
		Members:   []MemberRecord{feMember},
		Source:    &frontendEnv.Spec.Source,
		TTL:       &ttl,
	})
	require.NoError(t, err)
	assert.True(t, res1.Created)
	assert.Equal(t, "feat-checkout", res1.PreviewGroup.Name)
	assert.Equal(t, "feat-checkout", res1.PreviewGroup.Spec.Routing.HeaderValue)
	require.Len(t, res1.PreviewGroup.Spec.Services, 1)

	// 2. Backend MR #42 joins "feat-checkout" (plus baseline gateway) -> merges without clobbering web-app
	// and preserves Backend MR #42's own per-service Neon database config!
	beSvc, beMember, err := ServiceFromEnvironment(backendEnv)
	require.NoError(t, err)
	gwBaseline := v1alpha1.PreviewGroupServiceSpec{
		Name:      "gateway",
		Namespace: "backend",
		Mode:      v1alpha1.ServiceModeBaseline,
	}
	res2, err := Join(ctx, c, JoinOptions{
		GroupName: "feat-checkout",
		Services:  []v1alpha1.PreviewGroupServiceSpec{beSvc, gwBaseline},
		Members:   []MemberRecord{beMember},
	})
	require.NoError(t, err)
	assert.False(t, res2.Created)
	require.Len(t, res2.PreviewGroup.Spec.Services, 3)
	assert.Nil(t, res2.PreviewGroup.Spec.Database, "group-level DB should stay nil so per-service DB configs are preserved")
	require.NotNil(t, res2.PreviewGroup.Spec.Services[1].Database)
	assert.Equal(t, "neon", res2.PreviewGroup.Spec.Services[1].Database.Mode)
	assert.Len(t, res2.Members, 2)

	// 3. Backend MR #42 pushes a new commit -> SyncMemberEnvironment updates image automatically
	backendEnv.Spec.ServiceConfig.Image = "ghcr.io/acme/payments-api:sha-bbb"
	synced, err := SyncMemberEnvironment(ctx, c, backendEnv)
	require.NoError(t, err)
	assert.Equal(t, []string{"feat-checkout"}, synced)

	var current v1alpha1.PreviewGroup
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "feat-checkout"}, &current))
	assert.Equal(t, "ghcr.io/acme/payments-api:sha-bbb", current.Spec.Services[1].Image)
	assert.Equal(t, "ghcr.io/acme/web-app:sha-111", current.Spec.Services[0].Image)

	// 4. Backend MR #42 merges -> DetachMemberByPR removes payments-api, keeps web-app (+ baseline gateway)
	detached1, err := DetachMemberByPR(ctx, c, "acme/payments-api", 42, "")
	require.NoError(t, err)
	require.Len(t, detached1, 1)
	assert.False(t, detached1[0].DeletedGroup)
	assert.Equal(t, []string{"payments-api"}, detached1[0].RemovedServices)
	assert.Equal(t, []string{"web-app"}, detached1[0].RemainingServices)

	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "feat-checkout"}, &current))
	require.Len(t, current.Spec.Services, 2) // web-app + baseline gateway

	// 5. Frontend MR #101 merges (last active MR!) -> DetachMemberByPR deletes the entire PreviewGroup!
	detached2, err := DetachMemberByPR(ctx, c, "acme/web-app", 101, "")
	require.NoError(t, err)
	require.Len(t, detached2, 1)
	assert.True(t, detached2[0].DeletedGroup)

	err = c.Get(ctx, types.NamespacedName{Name: "feat-checkout"}, &current)
	assert.True(t, apierrors.IsNotFound(err), "PreviewGroup should be deleted when last active MR leaves")
}

func TestJoinSharedGroupDatabaseOverride(t *testing.T) {
	ctx := context.Background()
	c := newTestClient(t)

	svcWithOwnDB := v1alpha1.PreviewGroupServiceSpec{
		Name:      "payments-api",
		Namespace: "default",
		Mode:      v1alpha1.ServiceModeImage,
		Image:     "ghcr.io/acme/payments:pr-1",
		Database: &v1alpha1.EnvironmentDatabase{
			Mode: "schema",
		},
	}

	res, err := Join(ctx, c, JoinOptions{
		GroupName: "shared-db-group",
		Services:  []v1alpha1.PreviewGroupServiceSpec{svcWithOwnDB},
		GroupDatabase: &v1alpha1.EnvironmentDatabase{
			Mode:       "neon",
			SeedSource: "staging",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res.PreviewGroup.Spec.Database)
	assert.Equal(t, "neon", res.PreviewGroup.Spec.Database.Mode)
	assert.Nil(t, res.PreviewGroup.Spec.Services[0].Database, "group-level database override clears per-service database")
}
