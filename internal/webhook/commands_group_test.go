package webhook

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestParseGroupSlashCommands(t *testing.T) {
	cmd, ok := ParseSlashCommand("/diverge group feat/checkout")
	require.True(t, ok)
	assert.Equal(t, "group", cmd.Action)
	assert.Equal(t, "feat-checkout", cmd.GroupName)
	assert.Empty(t, cmd.DBMode)

	cmdDB, ok := ParseSlashCommand("/diverge group feat-checkout --db-mode neon")
	require.True(t, ok)
	assert.Equal(t, "group", cmdDB.Action)
	assert.Equal(t, "feat-checkout", cmdDB.GroupName)
	assert.Equal(t, "neon", cmdDB.DBMode)

	_, ok = ParseSlashCommand("/diverge group feat-checkout --db-mode invalid-db")
	assert.False(t, ok)

	unCmd, ok := ParseSlashCommand("/diverge ungroup")
	require.True(t, ok)
	assert.Equal(t, "ungroup", unCmd.Action)
	assert.Empty(t, unCmd.GroupName)

	unNamed, ok := ParseSlashCommand("/diverge ungroup feat/checkout")
	require.True(t, ok)
	assert.Equal(t, "ungroup", unNamed.Action)
	assert.Equal(t, "feat-checkout", unNamed.GroupName)
}

func TestSlashCommandGroupMultiMRLifecycle(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	feEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      previewEnvName("pr", "acme/web-app", 101),
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "acme/web-app",
				MR:       101,
				Branch:   "feat/checkout",
			},
			Routing: v1alpha1.EnvironmentRouting{
				ExternalURL: "https://staging.acme.dev",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "web-app",
				Namespace:   "default",
				Image:       "ghcr.io/acme/web-app:sha-101",
				Port:        3000,
			},
		},
	}

	beEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      previewEnvName("pr", "acme/payments-api", 42),
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "acme/payments-api",
				MR:       42,
				Branch:   "feat/checkout",
			},
			Database: v1alpha1.EnvironmentDatabase{
				Mode: "schema",
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				ServiceName: "payments-api",
				Namespace:   "default",
				Image:       "ghcr.io/acme/payments-api:sha-042",
				Port:        8080,
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feEnv, beEnv).Build()

	// 1. Frontend PR #101 comments `/diverge group feat/checkout`
	cmdJoinFE, ok := ParseSlashCommand("/diverge group feat/checkout")
	require.True(t, ok)
	msgFE, err := ExecuteSlashCommandWithFeedback(ctx, c, "default", cmdJoinFE, "https://github.com/acme/web-app", "feat/checkout", 101)
	require.NoError(t, err)
	assert.Contains(t, msgFE, "Created & Joined Multi-MR PreviewGroup: `feat-checkout`")
	assert.Contains(t, msgFE, "https://staging.acme.dev/?_diverge_env=feat-checkout")

	// 2. Backend PR #42 comments `/diverge group feat-checkout`
	cmdJoinBE, ok := ParseSlashCommand("/diverge group feat-checkout")
	require.True(t, ok)
	msgBE, err := ExecuteSlashCommandWithFeedback(ctx, c, "default", cmdJoinBE, "https://github.com/acme/payments-api", "feat/checkout", 42)
	require.NoError(t, err)
	assert.Contains(t, msgBE, "Joined Multi-MR PreviewGroup: `feat-checkout`")
	assert.Contains(t, msgBE, "`acme/web-app#101`")
	assert.Contains(t, msgBE, "`acme/payments-api#42`")

	var pg v1alpha1.PreviewGroup
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "feat-checkout"}, &pg))
	require.Len(t, pg.Spec.Services, 2)
	assert.Nil(t, pg.Spec.Database)
	require.NotNil(t, pg.Spec.Services[1].Database)
	assert.Equal(t, "schema", pg.Spec.Services[1].Database.Mode)

	// 3. `/diverge status` on Backend PR #42 shows Shared Multi-MR Preview Groups
	cmdStatus, _ := ParseSlashCommand("/diverge status")
	statusOut, err := ExecuteSlashCommandWithFeedback(ctx, c, "default", cmdStatus, "https://github.com/acme/payments-api", "feat/checkout", 42)
	require.NoError(t, err)
	assert.Contains(t, statusOut, "Shared Multi-MR Preview Groups")
	assert.Contains(t, statusOut, "`feat-checkout`")

	// 4. Backend PR #42 closes via GitHub webhook -> detaches payments-api, keeps web-app
	ghHandler := &GitHubWebhookHandler{Client: c, DefaultNS: "default"}
	closePayload := &GitHubPRPayload{}
	closePayload.Repository.FullName = "acme/payments-api"
	closePayload.PullRequest.Number = 42
	require.NoError(t, ghHandler.deleteEnvironment(ctx, closePayload))

	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "feat-checkout"}, &pg))
	require.Len(t, pg.Spec.Services, 1)
	assert.Equal(t, "web-app", pg.Spec.Services[0].Name)

	// 5. Frontend PR #101 comments `/diverge ungroup` -> last active MR leaves, deletes PreviewGroup
	cmdUngroup, ok := ParseSlashCommand("/diverge ungroup")
	require.True(t, ok)
	unMsg, err := ExecuteSlashCommandWithFeedback(ctx, c, "default", cmdUngroup, "https://github.com/acme/web-app", "feat/checkout", 101)
	require.NoError(t, err)
	assert.Contains(t, unMsg, "deleted group")

	err = c.Get(ctx, types.NamespacedName{Name: "feat-checkout"}, &pg)
	assert.True(t, apierrors.IsNotFound(err))
}
