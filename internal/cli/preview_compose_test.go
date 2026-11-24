package cli

import (
	"bytes"
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/previewgroup"
)

func TestPreviewJoinAndLeaveCommands(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	feEnv := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-acme-web-101",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "acme/web",
				MR:       101,
				Branch:   "feat/checkout",
			},
			ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{
				ServiceName: "web-app",
				Namespace:   "default",
				Image:       "ghcr.io/acme/web:pr-101",
				Port:        3000,
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feEnv).Build()
	app := &App{Namespace: "default", Client: c}

	// 1. Join Frontend via --from-env
	joinFE := newPreviewJoinCmd(app)
	var outFE bytes.Buffer
	joinFE.SetOut(&outFE)
	joinFE.SetArgs([]string{
		"feat/checkout",
		"--from-env", "pr-acme-web-101",
		"--base-url", "https://staging.acme.dev",
	})
	require.NoError(t, joinFE.Execute())
	assert.Contains(t, outFE.String(), `Created PreviewGroup "feat-checkout"`)

	// 2. Join Backend via --service + --project + --mr
	joinBE := newPreviewJoinCmd(app)
	var outBE bytes.Buffer
	joinBE.SetOut(&outBE)
	joinBE.SetArgs([]string{
		"feat-checkout",
		"--service", "payments-api=ghcr.io/acme/payments:pr-42:8080",
		"--project", "acme/payments",
		"--mr", "42",
		"--branch", "feat/checkout",
	})
	require.NoError(t, joinBE.Execute())
	assert.Contains(t, outBE.String(), `Joined PreviewGroup "feat-checkout" (2 total services)`)

	var pg divergeiov1alpha1.PreviewGroup
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "feat-checkout"}, &pg))
	require.Len(t, pg.Spec.Services, 2)
	assert.Equal(t, "https://staging.acme.dev/?_diverge_env=feat-checkout", pg.Spec.Routing.ExternalURL)

	// Verify preview status shows Members
	var statusBuf bytes.Buffer
	require.NoError(t, runPreviewStatus(context.Background(), app, "feat-checkout", &statusBuf))
	assert.Contains(t, statusBuf.String(), "Members:")
	assert.Contains(t, statusBuf.String(), "acme/web#101")
	assert.Contains(t, statusBuf.String(), "acme/payments#42")

	// 3. Leave Backend by --project + --mr -> keeps web-app running
	leaveBE := newPreviewLeaveCmd(app)
	var outLeaveBE bytes.Buffer
	leaveBE.SetOut(&outLeaveBE)
	leaveBE.SetArgs([]string{"feat-checkout", "--project", "acme/payments", "--mr", "42"})
	require.NoError(t, leaveBE.Execute())
	assert.Contains(t, outLeaveBE.String(), "remaining active: [web-app]")

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "feat-checkout"}, &pg))
	require.Len(t, pg.Spec.Services, 1)
	assert.Equal(t, "web-app", pg.Spec.Services[0].Name)

	// 4. Leave Frontend (last active service) -> auto-deletes PreviewGroup
	leaveFE := newPreviewLeaveCmd(app)
	var outLeaveFE bytes.Buffer
	leaveFE.SetOut(&outLeaveFE)
	leaveFE.SetArgs([]string{"feat-checkout", "--service", "web-app"})
	require.NoError(t, leaveFE.Execute())
	assert.Contains(t, outLeaveFE.String(), "group was deleted")

	err := c.Get(context.Background(), types.NamespacedName{Name: "feat-checkout"}, &pg)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestPreviewLeaveCommand_ProviderValidationAndGitFallbackWithExplicitProject(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "shared-prov",
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "gl-api", Namespace: "default", Mode: "image", Image: "img:gl-7"},
				{Name: "gh-api", Namespace: "default", Mode: "image", Image: "img:gh-7"},
			},
		},
	}
	require.NoError(t, previewgroup.SetMembers(pg, []previewgroup.MemberRecord{
		{Service: "gl-api", Namespace: "default", Provider: "gitlab", Project: "acme/api", MR: 7},
		{Service: "gh-api", Namespace: "default", Provider: "github", Project: "acme/api", MR: 7},
	}))

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pg).Build()
	app := &App{Namespace: "default", Client: c}

	// 1. Reject --provider local on MR detach
	invalidCmd := newPreviewLeaveCmd(app)
	invalidCmd.SetArgs([]string{"shared-prov", "--mr", "7", "--provider", "local"})
	err := invalidCmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "github or gitlab")

	// 2. --project acme/api --mr 7 inside a GitHub checkout detects provider=github and preserves gitlab member
	repoDir := t.TempDir()
	require.NoError(t, exec.Command("git", "init", repoDir).Run())
	require.NoError(t, exec.Command("git", "-C", repoDir, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "init").Run())
	require.NoError(t, exec.Command("git", "-C", repoDir, "remote", "add", "origin", "https://github.com/acme/api.git").Run())
	t.Chdir(repoDir)

	leaveCmd := newPreviewLeaveCmd(app)
	var out bytes.Buffer
	leaveCmd.SetOut(&out)
	leaveCmd.SetArgs([]string{"shared-prov", "--project", "acme/api", "--mr", "7"})
	require.NoError(t, leaveCmd.Execute())
	assert.Contains(t, out.String(), "remaining active: [gl-api]")

	var updated divergeiov1alpha1.PreviewGroup
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "shared-prov"}, &updated))
	require.Len(t, updated.Spec.Services, 1)
	assert.Equal(t, "gl-api", updated.Spec.Services[0].Name)
}
