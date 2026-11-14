package server

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server/auth"
)

func TestClusterService_GetCostSummary_EndToEndAttribution(t *testing.T) {
	c, k8s, logger, audit := setupClusterAuthTest(t)
	k8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = true
		return true, sar, nil
	})

	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "alice"})
	now := time.Now().UTC()

	// Configure namespace quota on "payments" ($0.050/hr budget, max 5 active)
	_, err := k8s.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "payments",
			Annotations: map[string]string{
				"divergedev.com/max-active-environments": "5",
				"divergedev.com/max-hourly-cost-usd":     "$0.050",
				"divergedev.com/quota-exceed-policy":     "hibernate-oldest",
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// 4 baseline microservice deployments in "payments" so delta-mode previews avoid 2-3 services!
	for _, depName := range []string{"gateway", "payments-api", "ledger-worker", "fraud-engine"} {
		_, depErr := k8s.AppsV1().Deployments("payments").Create(ctx, &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      depName,
				Namespace: "payments",
			},
		}, metav1.CreateOptions{})
		require.NoError(t, depErr)
	}

	// Active 2-service preview with schema DB (running for 2 hours):
	// Compute = 2 * (0.25*0.04 + 0.5*0.004) = 2 * 0.012 = $0.024/hr
	// DB schema = $0.010/hr -> Total = $0.034/hr (68% of $0.050/hr)
	// Let's also add a second active preview in "payments" ($0.012/hr) -> $0.046/hr (92% of $0.050/hr -> budget_warning=true!)
	require.NoError(t, c.Create(ctx, &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-101",
			Namespace:         "payments",
			CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour)),
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Project: "divergedev/bank",
				MR:      101,
				Branch:  "feat/instant-payouts",
			},
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"payments-api", "ledger-worker"},
			},
			Database: v1alpha1.EnvironmentDatabase{
				Mode: "schema",
			},
			Lifecycle: v1alpha1.EnvironmentLifecycle{
				TTL: &metav1.Duration{Duration: 24 * time.Hour},
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			Phase: v1alpha1.PhaseRunning,
		},
	}))

	// Second active preview in "payments" with NO TTL configured -> triggers ttl_warning!
	require.NoError(t, c.Create(ctx, &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-102",
			Namespace:         "payments",
			CreationTimestamp: metav1.NewTime(now.Add(-1 * time.Hour)),
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Project: "divergedev/bank",
				MR:      102,
				Branch:  "fix/retry-backoff",
			},
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"payments-api"},
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			Phase: v1alpha1.PhaseRunning,
		},
	}))

	// Hibernated preview in "checkout": $0.000/hr active, $0.012/hr hibernation savings
	require.NoError(t, c.Create(ctx, &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-88-idle",
			Namespace:         "checkout",
			CreationTimestamp: metav1.NewTime(now.Add(-5 * time.Hour)),
			Annotations: map[string]string{
				"divergedev.com/hibernated": "true",
			},
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Project: "divergedev/store",
				MR:      88,
				Branch:  "chore/cart-copy",
			},
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"checkout-ui"},
			},
			Lifecycle: v1alpha1.EnvironmentLifecycle{
				TTL: &metav1.Duration{Duration: 12 * time.Hour},
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			Phase: v1alpha1.PhaseRunning,
		},
	}))

	// AgentTask with $1.75 LLM spend in "payments"
	require.NoError(t, c.Create(ctx, &v1alpha1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-fix-101",
			Namespace: "payments",
		},
		Status: v1alpha1.AgentTaskStatus{
			Phase:   v1alpha1.AgentTaskPhaseCompleted,
			CostUSD: "1.75",
		},
	}))

	svc := NewClusterService(c, k8s, logger, audit, "v0.24.0")
	res, err := svc.GetCostSummary(ctx, connect.NewRequest(&pb.GetCostSummaryRequest{}))
	require.NoError(t, err)
	msg := res.Msg

	assert.Equal(t, int32(3), msg.TotalEnvironments)
	assert.Equal(t, int32(2), msg.ActiveEnvironments)
	assert.Equal(t, int32(1), msg.HibernatedEnvironments)

	// Total hourly: pr-101 ($0.034) + pr-102 ($0.012) + pr-88-idle ($0.000) = $0.046/hr
	assert.InDelta(t, 0.046, msg.TotalHourlyCostUsd, 1e-6)
	assert.InDelta(t, 0.046*730.0, msg.MonthlyRunRateUsd, 1e-4)
	assert.InDelta(t, 0.012, msg.HibernationSavingsHourlyUsd, 1e-6)

	// Delta savings against 4 baseline services in "payments":
	// pr-101 avoids (4-2)=2 services * $0.012 = $0.024/hr
	// pr-102 avoids (4-1)=3 services * $0.012 = $0.036/hr -> Total delta savings = $0.060/hr
	assert.InDelta(t, 0.060, msg.DeltaRoutingSavingsHourlyUsd, 1e-6)
	assert.InDelta(t, 1.75, msg.AgentTaskCostUsd, 1e-6)

	// Total active CPU: (2 + 1) * 0.25 = 0.75 cores; Memory: (2 + 1) * 0.5 = 1.5 GiB
	assert.InDelta(t, 0.75, msg.TotalCpuCores, 1e-6)
	assert.InDelta(t, 1.5, msg.TotalMemoryGib, 1e-6)
	// Active compute hours: pr-101 (2h * 2 svcs = 4h) + pr-102 (1h * 1 svc = 1h) = ~5.0h
	assert.InDelta(t, 5.0, msg.TotalComputeHours, 0.05)

	// Verify namespace breakdown & budget warning on "payments" ($0.046 / $0.050 = 92% >= 80%)
	require.Len(t, msg.ByNamespace, 2)
	assert.Equal(t, "payments", msg.ByNamespace[0].Namespace)
	assert.True(t, msg.ByNamespace[0].BudgetWarning)
	assert.InDelta(t, 92.0, msg.ByNamespace[0].BudgetUtilizationPercent, 0.5)
	assert.Contains(t, msg.ByNamespace[0].WarningReason, "Hourly spend")
	assert.Equal(t, "checkout", msg.ByNamespace[1].Namespace)
	assert.False(t, msg.ByNamespace[1].BudgetWarning)

	// Verify service breakdown sums cleanly to total hourly cost
	var svcSum float64
	for _, sb := range msg.ByService {
		svcSum += sb.HourlyCostUsd
	}
	assert.InDelta(t, msg.TotalHourlyCostUsd, svcSum, 1e-6)
	assert.Equal(t, "payments-api", msg.ByService[0].ServiceName)
	assert.Equal(t, int32(2), msg.ByService[0].EnvironmentCount)

	// Verify top consumers ordering and TTL warning on pr-102
	require.Len(t, msg.TopConsumers, 3)
	assert.Equal(t, "pr-101", msg.TopConsumers[0].Name)
	assert.False(t, msg.TopConsumers[0].TtlWarning)
	assert.Equal(t, "pr-102", msg.TopConsumers[1].Name)
	assert.True(t, msg.TopConsumers[1].TtlWarning)
	assert.Contains(t, msg.TopConsumers[1].TtlWarningReason, "No TTL configured")
	assert.Equal(t, "pr-88-idle", msg.TopConsumers[2].Name)
	assert.True(t, msg.TopConsumers[2].Hibernated)
}

func TestClusterService_GetCostSummary_NamespaceFilterAndRBAC(t *testing.T) {
	c, k8s, logger, audit := setupClusterAuthTest(t)

	// Only allow "list" on environments in namespace "team-a"; deny cluster-wide and "team-b"
	k8s.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = sar.Spec.ResourceAttributes != nil &&
			sar.Spec.ResourceAttributes.Namespace == "team-a" &&
			sar.Spec.ResourceAttributes.Resource == "environments"
		return true, sar, nil
	})

	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "bob"})
	require.NoError(t, c.Create(ctx, &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-a", Namespace: "team-a"},
		Status:     v1alpha1.EnvironmentStatus{Phase: v1alpha1.PhaseRunning},
	}))
	require.NoError(t, c.Create(ctx, &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-b", Namespace: "team-b"},
		Status:     v1alpha1.EnvironmentStatus{Phase: v1alpha1.PhaseRunning},
	}))

	svc := NewClusterService(c, k8s, logger, audit, "v0.24.0")

	// Cluster-wide request denied for namespace-scoped user
	_, err := svc.GetCostSummary(ctx, connect.NewRequest(&pb.GetCostSummaryRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// Invalid DNS label rejected with InvalidArgument
	_, err = svc.GetCostSummary(ctx, connect.NewRequest(&pb.GetCostSummaryRequest{Namespace: "INVALID_NS!"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// Scoped request to "team-a" succeeds even when agenttasks/previewgroups SAR is denied!
	res, err := svc.GetCostSummary(ctx, connect.NewRequest(&pb.GetCostSummaryRequest{Namespace: "team-a"}))
	require.NoError(t, err)
	assert.Equal(t, int32(1), res.Msg.TotalEnvironments)
	require.Len(t, res.Msg.TopConsumers, 1)
	assert.Equal(t, "pr-a", res.Msg.TopConsumers[0].Name)
}
