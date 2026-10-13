package notifier

import (
	"testing"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPreviewGroupTemplates_AllPhases(t *testing.T) {
	expiresAt := metav1.NewTime(time.Date(2026, 10, 12, 18, 0, 0, 0, time.UTC))

	makePreviewGroup := func(phase v1alpha1.PreviewGroupPhase, svcStatuses []v1alpha1.PreviewGroupServiceStatus) *v1alpha1.PreviewGroup {
		return &v1alpha1.PreviewGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pg-pr-42",
			},
			Spec: v1alpha1.PreviewGroupSpec{
				Source: v1alpha1.EnvironmentSource{
					Provider: "github",
					Project:  "divergedev/diverge",
					Branch:   "feat/preview-group",
					MR:       42,
				},
				Services: []v1alpha1.PreviewGroupServiceSpec{
					{
						Name:  "api",
						Mode:  v1alpha1.ServiceModeImage,
						Image: "ghcr.io/divergedev/api:pr-42",
					},
					{
						Name:     "worker",
						Mode:     v1alpha1.ServiceModeLocal,
						Endpoint: "100.64.0.10:8080",
					},
					{
						Name: "auth",
						Mode: v1alpha1.ServiceModeBaseline,
					},
				},
				Routing: v1alpha1.PreviewGroupRouting{
					HeaderKey:   "x-preview-env",
					HeaderValue: "pg-pr-42",
					ExternalURL: "https://preview.example.com",
				},
				Lifecycle: &v1alpha1.PreviewGroupLifecycle{
					TTL: &metav1.Duration{Duration: 4 * time.Hour},
				},
			},
			Status: v1alpha1.PreviewGroupStatus{
				Phase:     phase,
				ExpiresAt: &expiresAt,
				Services:  svcStatuses,
			},
		}
	}

	t.Run("Deploying phase template", func(t *testing.T) {
		pg := makePreviewGroup(v1alpha1.PreviewGroupPhaseDeploying, []v1alpha1.PreviewGroupServiceStatus{
			{Name: "api", Phase: v1alpha1.PhaseDeploying, Namespace: "preview-pr-42"},
			{Name: "worker", Phase: v1alpha1.PhasePending, Namespace: "staging"},
			{Name: "auth", Phase: v1alpha1.PhaseRunning, Namespace: "staging"},
		})

		data := buildPreviewGroupTemplateData(pg, "")
		assert.Equal(t, "pg-pr-42", data.Name)
		assert.Equal(t, "feat/preview-group", data.Branch)
		assert.Equal(t, 42, data.MR)
		assert.Equal(t, "4h0m0s", data.TTL)
		assert.Equal(t, 3, data.ServiceCount)
		assert.Equal(t, 1, data.RunningCount)

		out, err := renderTemplate(pgCreatedTemplate, data)
		require.NoError(t, err)
		assert.Contains(t, out, "## 🔀 Diverge Preview — Deploying")
		assert.Contains(t, out, "`pg-pr-42`")
		assert.Contains(t, out, "`feat/preview-group`")
		assert.Contains(t, out, "!42")
		assert.Contains(t, out, "| api | 📦 image | ghcr.io/divergedev/api:pr-42 |")
		assert.Contains(t, out, "| worker | 💻 local | 100.64.0.10:8080 |")
		assert.Contains(t, out, "| auth | ☁️ baseline | N/A (baseline) |")
		assert.Contains(t, out, "Expires in 4h0m0s")
	})

	t.Run("Running phase template", func(t *testing.T) {
		pg := makePreviewGroup(v1alpha1.PreviewGroupPhaseRunning, []v1alpha1.PreviewGroupServiceStatus{
			{Name: "api", Phase: v1alpha1.PhaseRunning, Namespace: "preview-pr-42"},
			{Name: "worker", Phase: v1alpha1.PhaseRunning, Namespace: "staging"},
			{Name: "auth", Phase: v1alpha1.PhaseRunning, Namespace: "staging"},
		})

		data := buildPreviewGroupTemplateData(pg, "")
		assert.Equal(t, 3, data.RunningCount)

		out, err := renderTemplate(pgReadyTemplate, data)
		require.NoError(t, err)
		assert.Contains(t, out, "## 🟢 Diverge Preview — Ready!")
		assert.Contains(t, out, "| **Status** | ✅ Running |")
		assert.Contains(t, out, "[🔗 Open Preview](https://preview.example.com?preview=pg-pr-42)")
		assert.Contains(t, out, "3 (3 running)")
		assert.Contains(t, out, "| ✅ api | Running | preview-pr-42 |")
		assert.Contains(t, out, `curl -H "x-preview-env: pg-pr-42" https://preview.example.com`)
	})

	t.Run("Degraded phase template", func(t *testing.T) {
		pg := makePreviewGroup(v1alpha1.PreviewGroupPhaseDegraded, []v1alpha1.PreviewGroupServiceStatus{
			{Name: "api", Phase: v1alpha1.PhaseRunning, Namespace: "preview-pr-42"},
			{Name: "worker", Phase: v1alpha1.PhaseFailed, Namespace: "staging", Reason: "HeartbeatExpired"},
			{Name: "auth", Phase: v1alpha1.PhaseRunning, Namespace: "staging"},
		})

		data := buildPreviewGroupTemplateData(pg, "worker degraded")
		assert.Equal(t, 2, data.RunningCount)

		out, err := renderTemplate(pgFailedTemplate, data)
		require.NoError(t, err)
		assert.Contains(t, out, "## 🔴 Diverge Preview — Failed")
		assert.Contains(t, out, "| **Status** | ❌ Failed |")
		assert.Contains(t, out, "| **Reason** | worker degraded |")
		assert.Contains(t, out, "| ✅ api | Running |  |")
		assert.Contains(t, out, "| ❌ worker | Failed | HeartbeatExpired |")
	})

	t.Run("Failed phase template", func(t *testing.T) {
		pg := makePreviewGroup(v1alpha1.PreviewGroupPhaseFailed, []v1alpha1.PreviewGroupServiceStatus{
			{Name: "api", Phase: v1alpha1.PhaseFailed, Namespace: "preview-pr-42", Reason: "ImagePullBackOff"},
			{Name: "worker", Phase: v1alpha1.PhasePending, Namespace: "staging"},
			{Name: "auth", Phase: v1alpha1.PhaseRunning, Namespace: "staging"},
		})

		data := buildPreviewGroupTemplateData(pg, "Service api failed to deploy")
		out, err := renderTemplate(pgFailedTemplate, data)
		require.NoError(t, err)
		assert.Contains(t, out, "## 🔴 Diverge Preview — Failed")
		assert.Contains(t, out, "| **Status** | ❌ Failed |")
		assert.Contains(t, out, "| **Reason** | Service api failed to deploy |")
		assert.Contains(t, out, "| ❌ api | Failed | ImagePullBackOff |")
	})

	t.Run("Teardown template", func(t *testing.T) {
		pg := makePreviewGroup(v1alpha1.PreviewGroupPhaseTerminating, nil)
		data := buildPreviewGroupTemplateData(pg, "PR merged")
		out, err := renderTemplate(pgTeardownTemplate, data)
		require.NoError(t, err)
		assert.Contains(t, out, "## 🗑️ Diverge Preview — Destroyed")
		assert.Contains(t, out, "Environment `pg-pr-42` has been cleaned up.")
		assert.Contains(t, out, "**Reason:** PR merged")
	})
}
