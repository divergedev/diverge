package finops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/internal/metrics"
)

func TestBudgetAlertDispatcher_WarningEscalationCooldownAndReset(t *testing.T) {
	var received []BudgetAlertPayload
	var hitCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var p BudgetAlertPayload
		require.NoError(t, json.NewDecoder(r.Body).Decode(&p))
		received = append(received, p)
		hitCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 15, 20, 0, 0, 0, time.UTC)
	dispatcher := NewBudgetAlertDispatcher(srv.URL, srv.Client())
	dispatcher.Cooldown = time.Hour
	dispatcher.NowFunc = func() time.Time { return now }

	// 1. 60% utilization -> below 80% threshold, no alert
	sent, sev, _, err := dispatcher.EvaluateAndNotify(context.Background(), nil, BudgetAlertInput{
		Namespace:        "payments",
		Environment:      "pr-1",
		HourlyCostUSD:    0.030,
		MaxHourlyCostUSD: 0.050,
	})
	require.NoError(t, err)
	assert.False(t, sent)
	assert.Empty(t, sev)
	assert.Equal(t, int32(0), hitCount.Load())

	// 2. 84% utilization -> crosses 80% warning threshold -> sends warning!
	sent, sev, reason, err := dispatcher.EvaluateAndNotify(context.Background(), nil, BudgetAlertInput{
		Namespace:        "payments",
		Environment:      "pr-2",
		HourlyCostUSD:    0.042,
		MaxHourlyCostUSD: 0.050,
		Policy:           "hibernate-oldest",
	})
	require.NoError(t, err)
	assert.True(t, sent)
	assert.Equal(t, SeverityWarning, sev)
	assert.Contains(t, reason, "84%")
	require.Len(t, received, 1)
	assert.Contains(t, received[0].Text, ":warning:")
	assert.Equal(t, "finops.budget_alert", received[0].Event)

	// 3. 10 minutes later at 90% -> still within 1h cooldown for warning -> suppressed!
	now = now.Add(10 * time.Minute)
	sent, sev, _, err = dispatcher.EvaluateAndNotify(context.Background(), nil, BudgetAlertInput{
		Namespace:        "payments",
		Environment:      "pr-2",
		HourlyCostUSD:    0.045,
		MaxHourlyCostUSD: 0.050,
	})
	require.NoError(t, err)
	assert.False(t, sent)
	assert.Equal(t, SeverityWarning, sev)
	assert.Equal(t, int32(1), hitCount.Load())

	// 4. 5 minutes later escalates to 108% (critical) -> bypasses warning cooldown and fires immediately!
	now = now.Add(5 * time.Minute)
	sent, sev, _, err = dispatcher.EvaluateAndNotify(context.Background(), nil, BudgetAlertInput{
		Namespace:        "payments",
		Environment:      "pr-3",
		HourlyCostUSD:    0.054,
		MaxHourlyCostUSD: 0.050,
		Exceeded:         true,
	})
	require.NoError(t, err)
	assert.True(t, sent)
	assert.Equal(t, SeverityCritical, sev)
	require.Len(t, received, 2)
	assert.Contains(t, received[1].Text, ":rotating_light:")

	// 5. Drops back to 40% -> resets hysteresis state!
	now = now.Add(5 * time.Minute)
	sent, _, _, err = dispatcher.EvaluateAndNotify(context.Background(), nil, BudgetAlertInput{
		Namespace:        "payments",
		HourlyCostUSD:    0.020,
		MaxHourlyCostUSD: 0.050,
	})
	require.NoError(t, err)
	assert.False(t, sent)

	// 6. Spikes back to 85% 2 minutes later -> fires warning immediately because state reset!
	now = now.Add(2 * time.Minute)
	sent, sev, _, err = dispatcher.EvaluateAndNotify(context.Background(), nil, BudgetAlertInput{
		Namespace:        "payments",
		Environment:      "pr-4",
		HourlyCostUSD:    0.0425,
		MaxHourlyCostUSD: 0.050,
	})
	require.NoError(t, err)
	assert.True(t, sent)
	assert.Equal(t, SeverityWarning, sev)
	assert.Equal(t, int32(3), hitCount.Load())
}

func TestBudgetAlertDispatcher_NamespaceSecretAndCustomThreshold(t *testing.T) {
	var gotPayload BudgetAlertPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotPayload))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "slack-finops-hook",
			Namespace: "checkout",
		},
		Data: map[string][]byte{
			"slack-url": []byte(srv.URL + "/services/T000/B000/secret-token"),
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	dispatcher := NewBudgetAlertDispatcher("", srv.Client())
	annotations := map[string]string{
		metrics.AnnotationBudgetAlertSecret:    "slack-finops-hook/slack-url",
		metrics.AnnotationBudgetAlertThreshold: "70",
	}

	sent, sev, _, err := dispatcher.EvaluateAndNotify(context.Background(), fakeClient, BudgetAlertInput{
		Namespace:             "checkout",
		Environment:           "pr-88",
		ActiveEnvironments:    3,
		MaxActiveEnvironments: 4, // 75% >= custom 70% threshold
		NamespaceAnnotations:  annotations,
	})
	require.NoError(t, err)
	assert.True(t, sent)
	assert.Equal(t, SeverityWarning, sev)
	assert.Equal(t, 75.0, gotPayload.UtilizationPercent)
	assert.Equal(t, 70.0, gotPayload.ThresholdPercent)
	// Missing key in explicitly configured Secret returns configuration error rather than falling back to global webhook
	badDispatcher := NewBudgetAlertDispatcher(srv.URL, srv.Client())
	sent, _, _, err = badDispatcher.EvaluateAndNotify(context.Background(), fakeClient, BudgetAlertInput{
		Namespace:             "checkout",
		Environment:           "pr-89",
		ActiveEnvironments:    4,
		MaxActiveEnvironments: 4,
		Exceeded:              true,
		NamespaceAnnotations: map[string]string{
			metrics.AnnotationBudgetAlertSecret: "slack-finops-hook/missing-key",
		},
	})
	require.Error(t, err)
	assert.False(t, sent)
	assert.Contains(t, err.Error(), "missing or empty")
}

func TestBudgetAlertDispatcher_RedactsWebhookURLOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	secretURL := srv.URL + "/services/T123/B456/SUPER_SECRET_SLACK_TOKEN"
	dispatcher := NewBudgetAlertDispatcher(secretURL, srv.Client())
	sent, _, _, err := dispatcher.EvaluateAndNotify(context.Background(), nil, BudgetAlertInput{
		Namespace:        "payments",
		HourlyCostUSD:    1.0,
		MaxHourlyCostUSD: 1.0,
		Exceeded:         true,
	})
	require.Error(t, err)
	assert.False(t, sent)
	assert.NotContains(t, err.Error(), "SUPER_SECRET_SLACK_TOKEN")

	// Malformed URL escape (#%zz) must also redact secret tokens from validation errors
	malformedDispatcher := NewBudgetAlertDispatcher("https://hooks.slack.com/services/T123/B456/SUPER_SECRET_SLACK_TOKEN#%zz", srv.Client())
	sent, _, _, err = malformedDispatcher.EvaluateAndNotify(context.Background(), nil, BudgetAlertInput{
		Namespace:        "payments",
		HourlyCostUSD:    1.0,
		MaxHourlyCostUSD: 1.0,
		Exceeded:         true,
	})
	require.Error(t, err)
	assert.False(t, sent)
	assert.NotContains(t, err.Error(), "SUPER_SECRET_SLACK_TOKEN")
}
