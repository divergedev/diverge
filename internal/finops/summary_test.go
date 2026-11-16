package finops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/metrics"
)

func TestBuildCostSummary_WithOpenCostEnrichmentAndFallback(t *testing.T) {
	openCostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 200,
			"data": [
				{
					"payments/pr-101": {
						"minutes": 60,
						"cpuCoreHours": 0.75,
						"ramByteHours": 1073741824,
						"totalCost": 0.055,
						"properties": {
							"namespace": "payments",
							"labels": {"divergedev.com/environment": "pr-101"}
						}
					}
				}
			]
		}`))
	}))
	defer openCostSrv.Close()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	now := time.Date(2026, 11, 15, 22, 0, 0, 0, time.UTC)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "payments",
			Annotations: map[string]string{
				AnnotationMaxHourlyCostUSD: "0.080",
			},
		},
	}
	// pr-101 has live OpenCost compute ($0.055/hr) + schema DB ($0.010/hr) = $0.065/hr (81.25% of $0.080/hr -> warning!)
	envLive := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-101",
			Namespace:         "payments",
			CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour)),
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source:   v1alpha1.EnvironmentSource{Project: "divergedev/bank", MR: 101, Branch: "feat/live"},
			Deploy:   v1alpha1.EnvironmentDeploy{ChangedServices: []string{"payments-api"}},
			Database: v1alpha1.EnvironmentDatabase{Mode: "schema"},
			Lifecycle: v1alpha1.EnvironmentLifecycle{
				TTL: &metav1.Duration{Duration: 24 * time.Hour},
			},
		},
		Status: v1alpha1.EnvironmentStatus{Phase: v1alpha1.PhaseRunning},
	}
	// pr-102 is NOT in OpenCost yet -> falls back seamlessly to RateCard ($0.012/hr)
	envFallback := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-102",
			Namespace:         "payments",
			CreationTimestamp: metav1.NewTime(now.Add(-time.Hour)),
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Project: "divergedev/bank", MR: 102, Branch: "feat/new"},
			Deploy: v1alpha1.EnvironmentDeploy{ChangedServices: []string{"gateway"}},
			Lifecycle: v1alpha1.EnvironmentLifecycle{
				TTL: &metav1.Duration{Duration: 24 * time.Hour},
			},
		},
		Status: v1alpha1.EnvironmentStatus{Phase: v1alpha1.PhaseRunning},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns, envLive, envFallback).Build()
	card := metrics.DefaultCostRateCard()

	summary, err := BuildCostSummary(context.Background(), SummaryOptions{
		Client:   c,
		Now:      now,
		RateCard: &card,
		OpenCost: NewOpenCostClient(openCostSrv.URL, openCostSrv.Client()),
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), summary.TotalEnvironments)
	assert.Equal(t, int32(2), summary.ActiveEnvironments)
	// Total = $0.065 (pr-101) + $0.012 (pr-102) = $0.077/hr
	assert.InDelta(t, 0.077, summary.TotalHourlyCostUsd, 1e-6)
	// Accrued preserves historical rate-card spend so hibernation never shrinks accrued spend:
	// pr-101 accrued (2h * $0.022 = $0.044) + pr-102 accrued (1h * $0.012 = $0.012) = $0.056
	assert.InDelta(t, 0.056, summary.TotalAccruedCostUsd, 1e-6)
	// CPU = 0.75 (from OpenCost) + 0.25 (RateCard default) = 1.00 cores
	assert.InDelta(t, 1.00, summary.TotalCpuCores, 1e-6)
	require.Len(t, summary.ByNamespace, 1)
	assert.True(t, summary.ByNamespace[0].BudgetWarning)
	assert.Contains(t, summary.ByNamespace[0].WarningReason, "96%")

	// Verify hibernating pr-101 at `now` preserves its exact AccruedCostUsd ($0.044) without shrinking
	envHib := envLive.DeepCopy()
	envHib.Annotations = metrics.RecordEnvironmentHibernated(envHib.Annotations, now)
	cHib := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns, envHib, envFallback).Build()
	summaryAfterHib, err := BuildCostSummary(context.Background(), SummaryOptions{
		Client:   cHib,
		Now:      now,
		RateCard: &card,
		OpenCost: NewOpenCostClient(openCostSrv.URL, openCostSrv.Client()),
	})
	require.NoError(t, err)
	assert.InDelta(t, summary.TotalAccruedCostUsd, summaryAfterHib.TotalAccruedCostUsd, 1e-6)
}

func TestBuildCostSummary_IsolatedCreateNamespaceAndEnvThreshold(t *testing.T) {
	t.Setenv(metrics.EnvBudgetAlertThreshold, "60")

	now := time.Date(2026, 11, 15, 22, 0, 0, 0, time.UTC)
	isolatedEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-isolated",
			Namespace:         "payments",
			CreationTimestamp: metav1.NewTime(now.Add(-time.Hour)),
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Project: "divergedev/bank", MR: 205, Branch: "feat/isolated"},
			Deploy: v1alpha1.EnvironmentDeploy{
				Namespace:       "create",
				ChangedServices: []string{"payments-api"},
			},
			Lifecycle: v1alpha1.EnvironmentLifecycle{
				TTL: &metav1.Duration{Duration: 24 * time.Hour},
			},
		},
		Status: v1alpha1.EnvironmentStatus{Phase: v1alpha1.PhaseRunning},
	}
	previewNS := isolatedEnv.PreviewNamespace()

	var seenFilter string
	openCostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 200,
			"data": [
				{
					"` + previewNS + `/pr-isolated": {
						"minutes": 60,
						"cpuCoreHours": 1.25,
						"ramByteHours": 2147483648,
						"totalCost": 0.065,
						"properties": {
							"namespace": "` + previewNS + `",
							"labels": {"divergedev.com/environment": "pr-isolated"}
						}
					}
				}
			]
		}`))
	}))
	defer openCostSrv.Close()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "payments",
			Annotations: map[string]string{
				// $0.065 / $0.100 = 65% (< default 80%, >= env threshold 60%!)
				AnnotationMaxHourlyCostUSD: "0.100",
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns, isolatedEnv).Build()
	card := metrics.DefaultCostRateCard()

	summary, err := BuildCostSummary(context.Background(), SummaryOptions{
		Namespace: "payments",
		Client:    c,
		Now:       now,
		RateCard:  &card,
		OpenCost:  NewOpenCostClient(openCostSrv.URL, openCostSrv.Client()),
	})
	require.NoError(t, err)
	assert.Contains(t, seenFilter, previewNS)
	assert.InDelta(t, 0.065, summary.TotalHourlyCostUsd, 1e-6)
	assert.InDelta(t, 1.25, summary.TotalCpuCores, 1e-6)
	assert.InDelta(t, 2.0, summary.TotalMemoryGib, 1e-6)
	require.Len(t, summary.ByNamespace, 1)
	assert.True(t, summary.ByNamespace[0].BudgetWarning, "DIVERGE_BUDGET_ALERT_THRESHOLD=60 should trigger warning at 65% utilization")
}
