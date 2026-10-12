package v1alpha1

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEnvironmentDeepCopyProperty(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		provider := hegel.Draw(ht, hegel.SampledFrom([]string{"gitlab", "github", "local"}))
		envName := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(80))
		namespace := hegel.Draw(ht, hegel.SampledFrom([]string{"default", "staging", "production"}))
		envKey := hegel.Draw(ht, hegel.SampledFrom([]string{"API_KEY", "DB_HOST", "LOG_LEVEL"}))
		envVal := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(40))
		svcName := hegel.Draw(ht, hegel.SampledFrom([]string{"api-svc", "auth-svc", "worker-svc"}))
		condMsg := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(40))

		env := &Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      envName,
				Namespace: namespace,
			},
			Spec: EnvironmentSpec{
				Source: EnvironmentSource{
					Provider: provider,
					Branch:   "feat/test",
				},
				Deploy: EnvironmentDeploy{
					ChangedServices: []string{svcName},
					NamespaceLabels: map[string]string{
						"team": "platform",
					},
				},
				ServiceConfig: &ServicePreviewConfig{
					ServiceName: svcName,
					Env: []EnvVar{
						{Name: envKey, Value: envVal},
					},
				},
				Features: &FeatureSpec{
					Provider: "configmap",
					Overrides: map[string]string{
						"new_ui": "true",
					},
				},
			},
			Status: EnvironmentStatus{
				Phase:    PhaseRunning,
				Services: []string{svcName},
				Conditions: []metav1.Condition{
					{
						Type:    "Ready",
						Status:  metav1.ConditionTrue,
						Reason:  "Provisioned",
						Message: condMsg,
					},
				},
				AsyncEnvVars: map[string]string{
					"TEMPORAL_TASK_QUEUE": "q-1",
				},
			},
		}

		copied := env.DeepCopy()
		require.NotSame(ht, env, copied)
		require.Equal(ht, env.Spec.Source.Provider, copied.Spec.Source.Provider)
		require.Equal(ht, envVal, copied.Spec.ServiceConfig.Env[0].Value)
		require.Equal(ht, condMsg, copied.Status.Conditions[0].Message)

		// Mutate slices and maps on copy and verify original is untouched
		copied.Spec.Deploy.ChangedServices[0] = "mutated-svc"
		copied.Spec.Deploy.NamespaceLabels["team"] = "mutated-team"
		copied.Spec.ServiceConfig.Env[0].Value = "mutated-val"
		copied.Spec.Features.Overrides["new_ui"] = "false"
		copied.Status.Services[0] = "mutated-status-svc"
		copied.Status.Conditions[0].Message = "mutated-msg"
		copied.Status.AsyncEnvVars["TEMPORAL_TASK_QUEUE"] = "mutated-q"

		require.Equal(ht, svcName, env.Spec.Deploy.ChangedServices[0])
		require.Equal(ht, "platform", env.Spec.Deploy.NamespaceLabels["team"])
		require.Equal(ht, envVal, env.Spec.ServiceConfig.Env[0].Value)
		require.Equal(ht, "true", env.Spec.Features.Overrides["new_ui"])
		require.Equal(ht, svcName, env.Status.Services[0])
		require.Equal(ht, condMsg, env.Status.Conditions[0].Message)
		require.Equal(ht, "q-1", env.Status.AsyncEnvVars["TEMPORAL_TASK_QUEUE"])

		// Verify PreviewNamespace() bounds, prefix, determinism, and cross-namespace isolation
		previewNS := env.PreviewNamespace()
		require.LessOrEqual(ht, len(previewNS), 63)
		require.True(ht, strings.HasPrefix(previewNS, "diverge"))
		require.Equal(ht, previewNS, env.PreviewNamespace())

		otherNS := env.DeepCopy()
		otherNS.Namespace = namespace + "-other"
		require.NotEqual(ht, previewNS, otherNS.PreviewNamespace())
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
		envKey := hegel.Draw(ht, hegel.SampledFrom([]string{"PORT", "STAGE", "FEATURE_X"}))
		envVal := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(30))
		condMsg := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(40))

		pg := &PreviewGroup{
			Spec: PreviewGroupSpec{
				Services: []PreviewGroupServiceSpec{
					{
						Name: svcName,
						Env: []EnvVar{
							{Name: envKey, Value: envVal},
						},
					},
				},
				TopologyOverrides: []TopologyEdge{
					{From: "gateway", To: svcName, Protocol: "http"},
				},
			},
			Status: PreviewGroupStatus{
				Phase: phase,
				Services: []PreviewGroupServiceStatus{
					{
						Name:            svcName,
						Phase:           PhaseRunning,
						ChangedServices: []string{svcName},
					},
				},
				Conditions: []metav1.Condition{
					{
						Type:    "Ready",
						Status:  metav1.ConditionTrue,
						Reason:  "AllServicesReady",
						Message: condMsg,
					},
				},
				DiscoveredIngressPaths: []DiscoveredIngressPath{
					{
						Entrypoint: "gateway",
						Target:     svcName,
						Hops:       []string{"gateway", svcName},
					},
				},
			},
		}

		copied := pg.DeepCopy()
		require.NotSame(ht, pg, copied)
		require.Equal(ht, pg.Status.Phase, copied.Status.Phase)
		require.Len(ht, copied.Spec.Services, 1)
		require.Equal(ht, svcName, copied.Spec.Services[0].Name)
		require.Equal(ht, envVal, copied.Spec.Services[0].Env[0].Value)
		require.Equal(ht, condMsg, copied.Status.Conditions[0].Message)

		// Mutate slices on copy and verify original is untouched
		copied.Spec.Services[0].Name = "mutated"
		copied.Spec.Services[0].Env[0].Value = "mutated-env"
		copied.Spec.TopologyOverrides[0].To = "mutated-to"
		copied.Status.Services[0].ChangedServices[0] = "mutated-changed"
		copied.Status.Conditions[0].Message = "mutated-cond"
		copied.Status.DiscoveredIngressPaths[0].Hops[1] = "mutated-hop"

		require.Equal(ht, svcName, pg.Spec.Services[0].Name)
		require.Equal(ht, envVal, pg.Spec.Services[0].Env[0].Value)
		require.Equal(ht, svcName, pg.Spec.TopologyOverrides[0].To)
		require.Equal(ht, svcName, pg.Status.Services[0].ChangedServices[0])
		require.Equal(ht, condMsg, pg.Status.Conditions[0].Message)
		require.Equal(ht, svcName, pg.Status.DiscoveredIngressPaths[0].Hops[1])
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
		repoURL := hegel.Draw(ht, hegel.SampledFrom([]string{
			"https://github.com/divergedev/diverge",
			"https://gitlab.com/org/service",
		}))
		baseBranch := hegel.Draw(ht, hegel.SampledFrom([]string{"main", "develop"}))
		workingBranch := hegel.Draw(ht, hegel.SampledFrom([]string{"agent/task-1", "agent/task-2"}))
		condMsg := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(40))

		task := &AgentTask{
			Spec: AgentTaskSpec{
				Objective: "Verify DeepCopy",
				Repository: AgentTaskRepository{
					URL:           repoURL,
					BaseBranch:    baseBranch,
					WorkingBranch: workingBranch,
				},
				Capabilities: []string{capTier},
			},
			Status: AgentTaskStatus{
				Phase: phase,
				Conditions: []metav1.Condition{
					{
						Type:    "SandboxReady",
						Status:  metav1.ConditionTrue,
						Reason:  "Bound",
						Message: condMsg,
					},
				},
			},
		}

		copied := task.DeepCopy()
		require.NotSame(ht, task, copied)
		require.Equal(ht, task.Status.Phase, copied.Status.Phase)
		require.Equal(ht, task.Spec.Repository, copied.Spec.Repository)
		require.Len(ht, copied.Spec.Capabilities, 1)
		require.Equal(ht, capTier, copied.Spec.Capabilities[0])
		require.Len(ht, copied.Status.Conditions, 1)
		require.Equal(ht, condMsg, copied.Status.Conditions[0].Message)

		// Mutate copy and verify original is untouched
		copied.Spec.Capabilities[0] = "mutated"
		copied.Spec.Repository.URL = "https://example.com/mutated"
		copied.Spec.Repository.BaseBranch = "mutated-base"
		copied.Spec.Repository.WorkingBranch = "mutated-work"
		copied.Status.Conditions[0].Message = "mutated-cond"

		require.Equal(ht, capTier, task.Spec.Capabilities[0])
		require.Equal(ht, repoURL, task.Spec.Repository.URL)
		require.Equal(ht, baseBranch, task.Spec.Repository.BaseBranch)
		require.Equal(ht, workingBranch, task.Spec.Repository.WorkingBranch)
		require.Equal(ht, condMsg, task.Status.Conditions[0].Message)
	})
}
