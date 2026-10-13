package metrics

import (
	"testing"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEstimateEnvironmentCost_Unit(t *testing.T) {
	created := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	now := created.Add(2 * time.Hour)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-42",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(created),
			Annotations: map[string]string{
				AnnotationHibernateAfter: "45m",
				AnnotationLastTrafficAt:  created.Add(30 * time.Minute).Format(time.RFC3339),
			},
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"api", "worker"},
			},
			Database: v1alpha1.EnvironmentDatabase{
				Mode: "schema",
			},
			Lifecycle: v1alpha1.EnvironmentLifecycle{
				TTL: &metav1.Duration{Duration: 10 * time.Hour},
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			Phase: v1alpha1.PhaseRunning,
		},
	}

	est := EstimateEnvironmentCost(env, now, DefaultCostRateCard())

	// 2 services * (0.25*0.04 + 0.5*0.004) = 2 * 0.012 = 0.024 + 0.01 schema = 0.034/hr
	assert.InDelta(t, 0.034, est.HourlyUSD, 1e-6)
	assert.InDelta(t, 0.068, est.AccruedUSD, 1e-6)
	assert.InDelta(t, 0.34, est.ProjectedTTLUSD, 1e-6)
	assert.Equal(t, 90*time.Minute, est.IdleDuration)
	assert.True(t, est.ShouldHibernate)

	DeleteEnvironmentCostMetric("pr-42", "default")
	assert.Equal(t, 0, testutil.CollectAndCount(EnvironmentEstimatedHourlyCostUSD))
}

func TestShouldHibernateIdleEnvironment(t *testing.T) {
	base := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-10",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(base),
		},
		Status: v1alpha1.EnvironmentStatus{
			Phase: v1alpha1.PhaseRunning,
		},
	}

	assert.False(t, ShouldHibernateIdleEnvironment(env, base, base.Add(15*time.Minute), 30*time.Minute))
	assert.True(t, ShouldHibernateIdleEnvironment(env, base, base.Add(31*time.Minute), 30*time.Minute))

	// Non-running environment should not hibernate
	env.Status.Phase = v1alpha1.PhaseDeploying
	assert.False(t, ShouldHibernateIdleEnvironment(env, base, base.Add(2*time.Hour), 30*time.Minute))
}

func TestEstimateEnvironmentCost_ResourceOverridesAndHibernated(t *testing.T) {
	created := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	now := created.Add(1 * time.Hour)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-resources",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(created),
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"api"},
			},
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				Resources: &v1alpha1.ResourceOverride{
					CPURequest:    "1",
					MemoryRequest: "2Gi",
				},
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			Phase: v1alpha1.PhaseRunning,
		},
	}

	est := EstimateEnvironmentCost(env, now, DefaultCostRateCard())
	// 1 vCPU * 0.04 + 2 GiB * 0.004 = 0.048/hr
	assert.InDelta(t, 0.048, est.HourlyUSD, 1e-6)

	// Mark hibernated -> hourlyUSD should be 0
	env.Annotations = map[string]string{
		AnnotationHibernated: "true",
	}
	estHibernated := EstimateEnvironmentCost(env, now, DefaultCostRateCard())
	assert.Equal(t, 0.0, estHibernated.HourlyUSD)

	DeleteEnvironmentCostMetric("pr-resources", "default")
}

func TestEstimateEnvironmentCost_PropertyMonotonicity(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	hegel.Test(t, func(ht *hegel.T) {
		svcCount1 := hegel.Draw(ht, hegel.Integers(1, 5))
		svcCount2 := hegel.Draw(ht, hegel.Integers(svcCount1, 10))
		minutes1 := hegel.Draw(ht, hegel.Integers(0, 600))
		minutes2 := hegel.Draw(ht, hegel.Integers(minutes1, 1200))

		svcs1 := make([]string, svcCount1)
		for i := range svcs1 {
			svcs1[i] = "svc"
		}
		svcs2 := make([]string, svcCount2)
		for i := range svcs2 {
			svcs2[i] = "svc"
		}

		env1 := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "env-1",
				Namespace:         "default",
				CreationTimestamp: metav1.NewTime(baseTime),
			},
			Spec: v1alpha1.EnvironmentSpec{
				Deploy: v1alpha1.EnvironmentDeploy{ChangedServices: svcs1},
			},
		}
		env2 := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "env-2",
				Namespace:         "default",
				CreationTimestamp: metav1.NewTime(baseTime),
			},
			Spec: v1alpha1.EnvironmentSpec{
				Deploy: v1alpha1.EnvironmentDeploy{ChangedServices: svcs2},
			},
		}

		est1 := EstimateEnvironmentCost(env1, baseTime.Add(time.Duration(minutes1)*time.Minute), DefaultCostRateCard())
		est2 := EstimateEnvironmentCost(env2, baseTime.Add(time.Duration(minutes2)*time.Minute), DefaultCostRateCard())
		DeleteEnvironmentCostMetric("env-1", "default")
		DeleteEnvironmentCostMetric("env-2", "default")

		require.GreaterOrEqual(ht, est1.HourlyUSD, 0.0)
		require.GreaterOrEqual(ht, est1.AccruedUSD, 0.0)
		require.GreaterOrEqual(ht, est2.HourlyUSD, est1.HourlyUSD, "more services must not decrease hourly cost")
		require.GreaterOrEqual(ht, est2.AccruedUSD, est1.AccruedUSD, "longer runtime and more services must not decrease accrued cost")
	})
}
