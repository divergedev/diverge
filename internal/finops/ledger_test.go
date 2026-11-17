package finops

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/metrics"
)

func ledgerTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, v1alpha1.AddToScheme(s))
	return s
}

func TestRecordEnvironmentTeardown_DedupPruneAndTrend(t *testing.T) {
	ctx := context.Background()
	scheme := ledgerTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	rateCard := metrics.DefaultCostRateCard()

	// 1. Expired record (>30 days old) should be pruned when a newer record arrives.
	staleEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			UID:               types.UID("uid-stale"),
			Name:              "pr-stale",
			Namespace:         "team-core",
			CreationTimestamp: metav1.NewTime(now.AddDate(0, 0, -40)),
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github", Branch: "feat/old", MR: 10},
			Deploy: v1alpha1.EnvironmentDeploy{ChangedServices: []string{"api"}},
		},
	}
	require.NoError(t, RecordEnvironmentTeardown(ctx, cl, staleEnv, now.AddDate(0, 0, -35), rateCard))

	// 2. Closed PR environment that ran 6 hours (4h active, 2h hibernated) on a remote spoke cluster.
	closedEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			UID:               types.UID("uid-closed-42"),
			Name:              "pr-42",
			Namespace:         "team-core",
			CreationTimestamp: metav1.NewTime(now.Add(-30 * time.Hour)),
			Annotations: map[string]string{
				AnnotationBaselineServiceCount:      "4",
				metrics.AnnotationHibernatedSeconds: "7200", // 2 hours
			},
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github", Project: "acme/shop", Branch: "feat/checkout", MR: 42},
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"checkout"},
				ClusterRef: &v1alpha1.ClusterTargetRef{
					Name: "us-west-burst",
					KubeconfigSecretRef: &v1alpha1.SecretRef{
						Name: "spoke-kubeconfig",
					},
				},
			},
			Database: v1alpha1.EnvironmentDatabase{Mode: "schema"},
		},
	}
	termAt := now.Add(-24 * time.Hour)
	require.NoError(t, RecordEnvironmentTeardown(ctx, cl, closedEnv, termAt, rateCard))
	// Recording twice with same UID is idempotent.
	require.NoError(t, RecordEnvironmentTeardown(ctx, cl, closedEnv, termAt, rateCard))

	records := ListHistoricalRecords(ctx, cl, nil, "team-core", now, 30, 2*time.Second)
	require.Len(t, records, 1, "stale >30d record pruned and duplicate UID collapsed")
	rec := records[0]
	assert.Equal(t, "pr-42", rec.Name)
	assert.Equal(t, "us-west-burst", rec.Cluster)
	assert.Equal(t, "42", rec.MR)
	assert.InDelta(t, 6.0, rec.UptimeHours, 0.01)
	assert.InDelta(t, 4.0, rec.ActiveHours, 0.01)
	assert.InDelta(t, 2.0, rec.HibernatedHours, 0.01)
	assert.Greater(t, rec.AccruedCostUSD, 0.0)
	assert.Greater(t, rec.HibernationSavingsUSD, 0.0)
	assert.Greater(t, rec.DeltaRoutingSavingsUSD, 0.0)

	// 3. Add a live federated environment and verify BuildCostSummary populates ByCluster, DailyTrend, and HistoricalConsumers.
	liveSpokeEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			UID:               types.UID("uid-live-99"),
			Name:              "pr-99",
			Namespace:         "team-core",
			CreationTimestamp: metav1.NewTime(now.Add(-3 * time.Hour)),
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github", Project: "acme/shop", Branch: "feat/cart", MR: 99},
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"cart"},
				ClusterRef: &v1alpha1.ClusterTargetRef{
					Name: "us-west-burst",
					KubeconfigSecretRef: &v1alpha1.SecretRef{
						Name: "spoke-kubeconfig",
					},
				},
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			Phase:         v1alpha1.PhaseRunning,
			TargetCluster: "us-west-burst",
		},
	}
	require.NoError(t, cl.Create(ctx, liveSpokeEnv))

	summary7d, err := BuildCostSummary(ctx, SummaryOptions{
		Namespace:         "team-core",
		Client:            cl,
		Now:               now,
		RateCard:          &rateCard,
		IncludeHistorical: true,
		WindowDays:        7,
	})
	require.NoError(t, err)
	require.Len(t, summary7d.ByCluster, 1)
	assert.Equal(t, "us-west-burst", summary7d.ByCluster[0].Cluster)
	assert.Equal(t, int32(1), summary7d.ByCluster[0].ActiveEnvironments)
	require.Len(t, summary7d.TopConsumers, 1)
	assert.Equal(t, "us-west-burst", summary7d.TopConsumers[0].Cluster)

	assert.Equal(t, int32(1), summary7d.HistoricalEnvironments)
	assert.InDelta(t, rec.AccruedCostUSD, summary7d.HistoricalAccruedCostUsd, 1e-6)
	assert.InDelta(t, rec.HibernationSavingsUSD+rec.DeltaRoutingSavingsUSD, summary7d.HistoricalSavingsUsd, 1e-6)
	require.Len(t, summary7d.HistoricalConsumers, 1)
	assert.True(t, summary7d.HistoricalConsumers[0].Historical)
	assert.Equal(t, "pr-42", summary7d.HistoricalConsumers[0].Name)
	assert.Equal(t, "us-west-burst", summary7d.HistoricalConsumers[0].Cluster)

	require.Len(t, summary7d.DailyTrend, 7)
	var trendSum float64
	var terminatedCount int32
	for _, pt := range summary7d.DailyTrend {
		trendSum += pt.AccruedCostUsd
		terminatedCount += pt.TerminatedEnvironments
	}
	assert.Equal(t, int32(1), terminatedCount)
	assert.InDelta(t, summary7d.TotalAccruedCostUsd+summary7d.HistoricalAccruedCostUsd, trendSum, 1e-6)
}

func TestMergeAndPruneLedger_MaxEntriesCap(t *testing.T) {
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -30)
	existing := make([]HistoricalEnvironmentRecord, 0, 10)
	for i := 0; i < 10; i++ {
		existing = append(existing, HistoricalEnvironmentRecord{
			UID:          fmt.Sprintf("u-%d", i),
			Name:         fmt.Sprintf("pr-%d", i),
			Namespace:    "ns",
			CreatedAt:    now.Add(-time.Duration(20-i) * time.Hour),
			TerminatedAt: now.Add(-time.Duration(10-i) * time.Hour),
		})
	}
	incoming := HistoricalEnvironmentRecord{
		UID:          "u-newest",
		Name:         "pr-newest",
		Namespace:    "ns",
		CreatedAt:    now.Add(-1 * time.Hour),
		TerminatedAt: now,
	}
	capped := mergeAndPruneLedger(existing, incoming, cutoff, 5)
	require.Len(t, capped, 5)
	assert.Equal(t, "pr-newest", capped[len(capped)-1].Name)
	assert.Equal(t, "pr-6", capped[0].Name)
}

type staticRoundTripper func(*http.Request) (*http.Response, error)

func (f staticRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestPreviewGroupSavingsAllocation_LiveTrendAndSequentialTeardown(t *testing.T) {
	ctx := context.Background()
	scheme := ledgerTestScheme(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	rateCard := metrics.DefaultCostRateCard()

	// 4-service PreviewGroup with 2 deployed child Environments (1 service each) and 2 baseline services.
	// Together they avoid 4 - 2 = 2 services (NOT 3 + 3 = 6).
	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pg-checkout",
			Namespace: "shop",
		},
		Spec: v1alpha1.PreviewGroupSpec{
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Image: "api:pr"},
				{Name: "web", Image: "web:pr"},
				{Name: "orders", Mode: v1alpha1.ServiceModeBaseline},
				{Name: "payments", Mode: v1alpha1.ServiceModeBaseline},
			},
		},
	}

	childA := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			UID:               types.UID("uid-pg-a"),
			Name:              "pg-checkout-api",
			Namespace:         "shop",
			CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour)),
			Labels: map[string]string{
				"divergedev.com/previewgroup": "pg-checkout",
			},
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github", Branch: "feat/pg"},
			Deploy: v1alpha1.EnvironmentDeploy{Namespace: "same", ChangedServices: []string{"api"}},
		},
		Status: v1alpha1.EnvironmentStatus{Phase: v1alpha1.PhaseRunning},
	}
	childB := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			UID:               types.UID("uid-pg-b"),
			Name:              "pg-checkout-web",
			Namespace:         "shop",
			CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour)),
			Labels: map[string]string{
				"divergedev.com/previewgroup": "pg-checkout",
			},
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github", Branch: "feat/pg"},
			Deploy: v1alpha1.EnvironmentDeploy{Namespace: "same", ChangedServices: []string{"web"}},
		},
		Status: v1alpha1.EnvironmentStatus{Phase: v1alpha1.PhaseRunning},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pg, childA, childB).Build()

	estSingle := metrics.ComputeEnvironmentCost(childA, now, rateCard)
	expectedGroupDeltaSavings := 2.0 * 2.0 * estSingle.ComputeHourlyUSD // 2h * (4 - 2 avoided svcs) * perSvcHourly

	// 1. Live dailyTrend must report group-level avoided savings (2 services, not 6).
	liveSummary, err := BuildCostSummary(ctx, SummaryOptions{
		Namespace:  "shop",
		Client:     cl,
		Now:        now,
		RateCard:   &rateCard,
		WindowDays: 7,
	})
	require.NoError(t, err)
	var liveTrendDeltaSum float64
	for _, pt := range liveSummary.DailyTrend {
		liveTrendDeltaSum += pt.DeltaRoutingSavingsUsd
	}
	assert.InDelta(t, expectedGroupDeltaSavings, liveTrendDeltaSavingsSumOrZero(liveTrendDeltaSum), 1e-6)

	// 2. Sequential teardown: childA tears down & is deleted from cluster BEFORE childB tears down.
	require.NoError(t, RecordEnvironmentTeardown(ctx, cl, childA, now, rateCard))
	require.NoError(t, cl.Delete(ctx, childA))

	// 2a. Mid-transition summary (childA in ledger, childB still live) must retain childA's 2r and keep childB at 2r (total 4r, NOT 8r).
	midSummary, err := BuildCostSummary(ctx, SummaryOptions{
		Namespace:         "shop",
		Client:            cl,
		Now:               now,
		RateCard:          &rateCard,
		IncludeHistorical: true,
		WindowDays:        7,
	})
	require.NoError(t, err)
	var midTrendDeltaSum float64
	for _, pt := range midSummary.DailyTrend {
		midTrendDeltaSum += pt.DeltaRoutingSavingsUsd
	}
	assert.InDelta(t, expectedGroupDeltaSavings, midTrendDeltaSum, 1e-6, "mid-teardown summary must not double-allocate avoided baseline services to surviving childB")

	require.NoError(t, RecordEnvironmentTeardown(ctx, cl, childB, now, rateCard))
	require.NoError(t, cl.Delete(ctx, childB))

	histSummary, err := BuildCostSummary(ctx, SummaryOptions{
		Namespace:         "shop",
		Client:            cl,
		Now:               now,
		RateCard:          &rateCard,
		IncludeHistorical: true,
		WindowDays:        7,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), histSummary.HistoricalEnvironments)
	assert.InDelta(t, expectedGroupDeltaSavings, histSummary.HistoricalSavingsUsd, 1e-6)
}

func TestPreviewGroupSavingsAllocation_StaggeredOverlapAndTerminalSibling(t *testing.T) {
	ctx := context.Background()
	scheme := ledgerTestScheme(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	rateCard := metrics.DefaultCostRateCard()

	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-staggered", Namespace: "shop"},
		Spec: v1alpha1.PreviewGroupSpec{
			Services: []v1alpha1.PreviewGroupServiceSpec{
				{Name: "api", Image: "api:pr"},
				{Name: "web", Image: "web:pr"},
				{Name: "orders", Mode: v1alpha1.ServiceModeBaseline},
				{Name: "payments", Mode: v1alpha1.ServiceModeBaseline},
			},
		},
	}

	// Subtest 1: A runs 10:00-12:00 (2h), B runs 11:00-12:00 (1h), baseline = 4.
	// 10:00-11:00: 1 deployed -> 3 avoided * r = 3r (all to A)
	// 11:00-12:00: 2 deployed -> 2 avoided * r = 2r (1r to A, 1r to B)
	// Total = 5r (A=4r, B=1r).
	childA := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			UID:               types.UID("uid-stag-a"),
			Name:              "pg-stag-api",
			Namespace:         "shop",
			CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour)),
			Labels:            map[string]string{"divergedev.com/previewgroup": "pg-staggered"},
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github", Branch: "feat/stag"},
			Deploy: v1alpha1.EnvironmentDeploy{Namespace: "same", ChangedServices: []string{"api"}},
		},
		Status: v1alpha1.EnvironmentStatus{Phase: v1alpha1.PhaseRunning},
	}
	childB := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			UID:               types.UID("uid-stag-b"),
			Name:              "pg-stag-web",
			Namespace:         "shop",
			CreationTimestamp: metav1.NewTime(now.Add(-1 * time.Hour)),
			Labels:            map[string]string{"divergedev.com/previewgroup": "pg-staggered"},
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github", Branch: "feat/stag"},
			Deploy: v1alpha1.EnvironmentDeploy{Namespace: "same", ChangedServices: []string{"web"}},
		},
		Status: v1alpha1.EnvironmentStatus{Phase: v1alpha1.PhaseRunning},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pg, childA, childB).Build()
	r := metrics.ComputeEnvironmentCost(childA, now, rateCard).BaseComputeHourlyUSD

	require.NoError(t, RecordEnvironmentTeardown(ctx, cl, childA, now, rateCard))
	require.NoError(t, cl.Delete(ctx, childA))
	require.NoError(t, RecordEnvironmentTeardown(ctx, cl, childB, now, rateCard))
	require.NoError(t, cl.Delete(ctx, childB))

	records := ListHistoricalRecords(ctx, cl, nil, "shop", now, 7, 2*time.Second)
	require.Len(t, records, 2)
	savingsByName := map[string]float64{}
	var totalStaggered float64
	for _, rec := range records {
		savingsByName[rec.Name] = rec.DeltaRoutingSavingsUSD
		totalStaggered += rec.DeltaRoutingSavingsUSD
	}
	assert.InDelta(t, 4.0*r, savingsByName["pg-stag-api"], 1e-6)
	assert.InDelta(t, 1.0*r, savingsByName["pg-stag-web"], 1e-6)
	assert.InDelta(t, 5.0*r, totalStaggered, 1e-6)

	// Subtest 2: Equal 2h children where sibling B entered PhaseFailed right at 12:00 (retaining 2h historical active runtime).
	// Phase-independent BaseComputeHourlyUSD must preserve full 4r group savings rather than halving to 2r.
	termA := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			UID:               types.UID("uid-term-a"),
			Name:              "pg-term-api",
			Namespace:         "shop-term",
			CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour)),
			Labels:            map[string]string{"divergedev.com/previewgroup": "pg-term"},
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github", Branch: "feat/term"},
			Deploy: v1alpha1.EnvironmentDeploy{Namespace: "same", ChangedServices: []string{"api"}},
		},
		Status: v1alpha1.EnvironmentStatus{Phase: v1alpha1.PhaseRunning},
	}
	termB := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			UID:               types.UID("uid-term-b"),
			Name:              "pg-term-web",
			Namespace:         "shop-term",
			CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour)),
			Labels:            map[string]string{"divergedev.com/previewgroup": "pg-term"},
			Annotations: map[string]string{
				metrics.AnnotationTerminatedAt: now.UTC().Format(time.RFC3339),
			},
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github", Branch: "feat/term"},
			Deploy: v1alpha1.EnvironmentDeploy{Namespace: "same", ChangedServices: []string{"web"}},
		},
		Status: v1alpha1.EnvironmentStatus{Phase: v1alpha1.PhaseFailed},
	}
	pgTerm := pg.DeepCopy()
	pgTerm.Name = "pg-term"
	pgTerm.Namespace = "shop-term"

	clTerm := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pgTerm, termA, termB).Build()
	termLiveSummary, err := BuildCostSummary(ctx, SummaryOptions{
		Namespace:  "shop-term",
		Client:     clTerm,
		Now:        now,
		RateCard:   &rateCard,
		WindowDays: 7,
	})
	require.NoError(t, err)
	var termLiveSum float64
	for _, pt := range termLiveSummary.DailyTrend {
		termLiveSum += pt.DeltaRoutingSavingsUsd
	}
	assert.InDelta(t, 4.0*r, termLiveSum, 1e-6)

	require.NoError(t, RecordEnvironmentTeardown(ctx, clTerm, termA, now, rateCard))
	require.NoError(t, clTerm.Delete(ctx, termA))
	require.NoError(t, RecordEnvironmentTeardown(ctx, clTerm, termB, now, rateCard))
	require.NoError(t, clTerm.Delete(ctx, termB))

	termRecs := ListHistoricalRecords(ctx, clTerm, nil, "shop-term", now, 7, 2*time.Second)
	require.Len(t, termRecs, 2)
	assert.InDelta(t, 4.0*r, termRecs[0].DeltaRoutingSavingsUSD+termRecs[1].DeltaRoutingSavingsUSD, 1e-6)
}

func liveTrendDeltaSavingsSumOrZero(v float64) float64 { return v }

func TestBuildCostSummary_LiveOpenCostDoesNotInflatePriorAccumulatedSavings(t *testing.T) {
	ctx := context.Background()
	scheme := ledgerTestScheme(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	rateCard := metrics.DefaultCostRateCard()

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			UID:               types.UID("uid-oc-1"),
			Name:              "pr-oc",
			Namespace:         "payments",
			CreationTimestamp: metav1.NewTime(now.Add(-4 * time.Hour)),
			Annotations: map[string]string{
				AnnotationBaselineServiceCount:      "3",
				metrics.AnnotationHibernatedSeconds: "7200", // 2h hibernated, 2h active
			},
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github", Branch: "feat/oc"},
			Deploy: v1alpha1.EnvironmentDeploy{Namespace: "same", ChangedServices: []string{"api"}},
		},
		Status: v1alpha1.EnvironmentStatus{Phase: v1alpha1.PhaseRunning},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(env).Build()

	// Baseline summary without OpenCost.
	withoutOC, err := BuildCostSummary(ctx, SummaryOptions{
		Namespace:  "payments",
		Client:     cl,
		Now:        now,
		RateCard:   &rateCard,
		WindowDays: 7,
	})
	require.NoError(t, err)

	// OpenCost reports a 50x current-hour spike ($5.00/hr for 60 minutes).
	ocClient := &OpenCostClient{
		baseURL: "https://opencost.internal",
		timeout: time.Second,
		httpClient: &http.Client{
			Transport: staticRoundTripper(func(_ *http.Request) (*http.Response, error) {
				payload := `{"code":200,"data":[{"payments/pr-oc":{"name":"payments/pr-oc","minutes":60,"totalCost":5.0,"cpuCoreUsageAverage":2.0,"ramByteUsageAverage":1073741824}}]}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(payload)),
				}, nil
			}),
		},
	}

	withOC, err := BuildCostSummary(ctx, SummaryOptions{
		Namespace:  "payments",
		Client:     cl,
		Now:        now,
		RateCard:   &rateCard,
		OpenCost:   ocClient,
		WindowDays: 7,
	})
	require.NoError(t, err)
	assert.True(t, withOC.OpencostEnabled)
	assert.InDelta(t, 5.0, withOC.TotalHourlyCostUsd, 1e-6)

	var baseSaved, ocSaved float64
	for _, pt := range withoutOC.DailyTrend {
		baseSaved += pt.HibernationSavingsUsd + pt.DeltaRoutingSavingsUsd
	}
	for _, pt := range withOC.DailyTrend {
		ocSaved += pt.HibernationSavingsUsd + pt.DeltaRoutingSavingsUsd
	}
	assert.InDelta(t, baseSaved, ocSaved, 1e-6, "instantaneous OpenCost hourly spike must not retroactively inflate prior-hour trend savings")
}
