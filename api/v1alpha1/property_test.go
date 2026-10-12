package v1alpha1

import (
	"testing"

	"hegel.dev/go/hegel"
)

func TestEnvironmentDeepCopyProperty(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		provider := hegel.Draw(ht, hegel.SampledFrom([]string{"gitlab", "github"}))
		env := &Environment{
			Spec: EnvironmentSpec{
				Source: EnvironmentSource{
					Provider: provider,
				},
			},
		}

		copied := env.DeepCopy()
		if copied.Spec.Source.Provider != env.Spec.Source.Provider {
			t.Errorf("DeepCopy failed for provider %s", provider)
		}
		if copied == env {
			t.Errorf("DeepCopy returned same pointer")
		}
	})
}

func TestPreviewGroupDeepCopyProperty(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		phase := hegel.Draw(ht, hegel.SampledFrom([]PreviewGroupPhase{
			PreviewGroupPhasePending,
			PreviewGroupPhaseDeploying,
			PreviewGroupPhaseRunning,
			PreviewGroupPhaseDegraded,
			PreviewGroupPhaseFailed,
			PreviewGroupPhaseTerminating,
			PreviewGroupPhaseAbandoned,
		}))
		svcName := hegel.Draw(ht, hegel.SampledFrom([]string{"api-svc", "auth-svc", "worker-svc"}))
		pg := &PreviewGroup{
			Spec: PreviewGroupSpec{
				Services: []PreviewGroupServiceSpec{
					{Name: svcName},
				},
			},
			Status: PreviewGroupStatus{
				Phase: phase,
			},
		}

		copied := pg.DeepCopy()
		if copied == pg {
			t.Errorf("DeepCopy returned same pointer")
		}
		if copied.Status.Phase != pg.Status.Phase {
			t.Errorf("DeepCopy failed for phase %s", phase)
		}
		if len(copied.Spec.Services) != 1 || copied.Spec.Services[0].Name != svcName {
			t.Errorf("DeepCopy failed for service %s", svcName)
		}
		copied.Spec.Services[0].Name = "mutated"
		if pg.Spec.Services[0].Name == "mutated" {
			t.Errorf("DeepCopy shared underlying Services slice")
		}
	})
}

func TestAgentTaskDeepCopyProperty(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		phase := hegel.Draw(ht, hegel.SampledFrom([]AgentTaskPhase{
			AgentTaskPhasePending,
			AgentTaskPhaseProvisioning,
			AgentTaskPhaseActive,
			AgentTaskPhasePausing,
			AgentTaskPhasePaused,
			AgentTaskPhaseCompleted,
			AgentTaskPhaseFailed,
		}))
		capTier := hegel.Draw(ht, hegel.SampledFrom([]string{"fast", "lite", "smart"}))
		task := &AgentTask{
			Spec: AgentTaskSpec{
				Objective:    "Verify DeepCopy",
				Capabilities: []string{capTier},
			},
			Status: AgentTaskStatus{
				Phase: phase,
			},
		}

		copied := task.DeepCopy()
		if copied == task {
			t.Errorf("DeepCopy returned same pointer")
		}
		if copied.Status.Phase != task.Status.Phase {
			t.Errorf("DeepCopy failed for phase %s", phase)
		}
		if len(copied.Spec.Capabilities) != 1 || copied.Spec.Capabilities[0] != capTier {
			t.Errorf("DeepCopy failed for capability %s", capTier)
		}
		copied.Spec.Capabilities[0] = "mutated"
		if task.Spec.Capabilities[0] == "mutated" {
			t.Errorf("DeepCopy shared underlying Capabilities slice")
		}
	})
}
