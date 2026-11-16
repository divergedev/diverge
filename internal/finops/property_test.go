package finops

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestProperty_ScaleEntryToHourlyIsMonotonicAndFinite(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		minutes := rapid.Float64Range(0.5, 1440.0).Draw(t, "minutes")
		costA := rapid.Float64Range(0.0, 500.0).Draw(t, "costA")
		delta := rapid.Float64Range(0.0, 500.0).Draw(t, "delta")
		cpuHrs := rapid.Float64Range(0.0, 64.0).Draw(t, "cpuHrs")
		ramBytes := rapid.Float64Range(0.0, 64.0*bytesPerGiB).Draw(t, "ramBytes")

		allocA, okA := scaleEntryToHourly(openCostAllocationEntry{
			Minutes:      minutes,
			TotalCost:    costA,
			CPUCoreHours: cpuHrs,
			RAMByteHours: ramBytes,
		})
		allocB, okB := scaleEntryToHourly(openCostAllocationEntry{
			Minutes:      minutes,
			TotalCost:    costA + delta,
			CPUCoreHours: cpuHrs,
			RAMByteHours: ramBytes,
		})

		if !okA || !okB {
			t.Fatalf("expected valid hourly scaling for finite inputs")
		}
		if math.IsNaN(allocA.HourlyComputeUSD) || math.IsInf(allocA.HourlyComputeUSD, 0) || allocA.HourlyComputeUSD < 0 {
			t.Fatalf("non-finite or negative hourly cost: %v", allocA.HourlyComputeUSD)
		}
		if allocB.HourlyComputeUSD+1e-9 < allocA.HourlyComputeUSD {
			t.Fatalf("expected monotonic cost scaling: %v < %v", allocB.HourlyComputeUSD, allocA.HourlyComputeUSD)
		}
	})
}

func TestProperty_BudgetAlertCooldownNeverSpamsWithinWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rapid.Check(t, func(t *rapid.T) {
		cooldownMins := rapid.IntRange(5, 120).Draw(t, "cooldownMins")
		threshold := rapid.Float64Range(50.0, 95.0).Draw(t, "threshold")
		steps := rapid.SliceOfN(rapid.Float64Range(0.0, 2.0), 1, 25).Draw(t, "burns")

		now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
		d := NewBudgetAlertDispatcher(srv.URL, srv.Client())
		d.DefaultThresholdPercent = threshold
		d.Cooldown = time.Duration(cooldownMins) * time.Minute
		d.NowFunc = func() time.Time { return now }

		var lastWarnSent, lastCritSent time.Time
		for _, burn := range steps {
			now = now.Add(time.Minute)
			sent, sev, _, err := d.EvaluateAndNotify(context.Background(), nil, BudgetAlertInput{
				Namespace:        "prop-ns",
				HourlyCostUSD:    burn,
				MaxHourlyCostUSD: 1.0,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			utilPct := burn * 100.0
			if utilPct < threshold {
				if sent {
					t.Fatalf("alert sent below threshold: util=%.2f threshold=%.2f", utilPct, threshold)
				}
				lastWarnSent = time.Time{}
				lastCritSent = time.Time{}
				continue
			}
			if sent {
				switch sev {
				case SeverityWarning:
					if !lastWarnSent.IsZero() && now.Sub(lastWarnSent) < d.Cooldown {
						t.Fatalf("warning alert spammed inside cooldown window: %v < %v", now.Sub(lastWarnSent), d.Cooldown)
					}
					lastWarnSent = now
				case SeverityCritical:
					if !lastCritSent.IsZero() && now.Sub(lastCritSent) < d.Cooldown {
						t.Fatalf("critical alert spammed inside cooldown window: %v < %v", now.Sub(lastCritSent), d.Cooldown)
					}
					lastCritSent = now
				}
			}
		}
	})
}
