package metrics

import (
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	AnnotationHibernateAfter = "divergedev.com/hibernate-after"
	AnnotationLastTrafficAt  = "divergedev.com/last-traffic-at"
	defaultIdleThreshold     = 30 * time.Minute
	defaultPreviewTTL        = 24 * time.Hour
)

// EnvironmentEstimatedHourlyCostUSD tracks the estimated hourly USD cost per preview environment.
var EnvironmentEstimatedHourlyCostUSD = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "diverge_environment_estimated_hourly_cost_usd",
		Help: "Estimated hourly cost in USD for each preview environment",
	},
	[]string{"environment", "namespace"},
)

func init() {
	ctrlmetrics.Registry.MustRegister(EnvironmentEstimatedHourlyCostUSD)
}

// CostRateCard defines the unit hourly resource pricing for preview environments.
type CostRateCard struct {
	VCPUHourUSD         float64
	MemoryGiBHourUSD    float64
	DatabaseSchemaHrUSD float64
}

// DefaultCostRateCard returns the standard unit rate card ($0.04/vCPU-hr, $0.004/GiB-hr, $0.01/schema-hr).
func DefaultCostRateCard() CostRateCard {
	return CostRateCard{
		VCPUHourUSD:         0.04,
		MemoryGiBHourUSD:    0.004,
		DatabaseSchemaHrUSD: 0.01,
	}
}

func normalizeRateCard(rate CostRateCard) CostRateCard {
	if rate.VCPUHourUSD == 0 && rate.MemoryGiBHourUSD == 0 && rate.DatabaseSchemaHrUSD == 0 {
		return DefaultCostRateCard()
	}
	if rate.VCPUHourUSD < 0 {
		rate.VCPUHourUSD = 0
	}
	if rate.MemoryGiBHourUSD < 0 {
		rate.MemoryGiBHourUSD = 0
	}
	if rate.DatabaseSchemaHrUSD < 0 {
		rate.DatabaseSchemaHrUSD = 0
	}
	return rate
}

// EnvironmentCostEstimate holds the computed cost and idle-hibernation recommendation for an Environment.
type EnvironmentCostEstimate struct {
	HourlyUSD       float64
	AccruedUSD      float64
	ProjectedTTLUSD float64
	IdleDuration    time.Duration
	ShouldHibernate bool
}

// EstimateEnvironmentCost computes the hourly, accrued, and projected TTL cost for a preview Environment.
func EstimateEnvironmentCost(env *v1alpha1.Environment, now time.Time, rate CostRateCard) EnvironmentCostEstimate {
	if env == nil {
		return EnvironmentCostEstimate{}
	}
	rate = normalizeRateCard(rate)

	serviceCount := len(env.Spec.Deploy.ChangedServices)
	if len(env.Status.Services) > serviceCount {
		serviceCount = len(env.Status.Services)
	}
	if serviceCount < 1 {
		serviceCount = 1
	}

	// Default per-service footprint: 0.25 vCPU and 0.5 GiB RAM
	perServiceHourly := 0.25*rate.VCPUHourUSD + 0.5*rate.MemoryGiBHourUSD
	hourlyUSD := float64(serviceCount) * perServiceHourly

	if env.Spec.Database.Mode != "" && env.Spec.Database.Mode != "none" {
		hourlyUSD += rate.DatabaseSchemaHrUSD
	}
	if hourlyUSD < 0 {
		hourlyUSD = 0
	}

	startTime := env.CreationTimestamp.Time
	if startTime.IsZero() && env.Status.CreatedAt != nil {
		startTime = env.Status.CreatedAt.Time
	}

	var runtimeHours float64
	if !startTime.IsZero() && now.After(startTime) {
		runtimeHours = now.Sub(startTime).Hours()
	}
	accruedUSD := hourlyUSD * runtimeHours

	ttl := defaultPreviewTTL
	if env.Spec.Lifecycle.TTL != nil && env.Spec.Lifecycle.TTL.Duration > 0 {
		ttl = env.Spec.Lifecycle.TTL.Duration
	}
	projectedTTLUSD := hourlyUSD * ttl.Hours()
	if projectedTTLUSD < accruedUSD {
		projectedTTLUSD = accruedUSD
	}

	lastTraffic := startTime
	if raw, ok := env.Annotations[AnnotationLastTrafficAt]; ok && raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			lastTraffic = parsed
		}
	}

	var idleDuration time.Duration
	if !lastTraffic.IsZero() && now.After(lastTraffic) {
		idleDuration = now.Sub(lastTraffic)
	}

	shouldHibernate := ShouldHibernateIdleEnvironment(env, lastTraffic, now, defaultIdleThreshold)

	EnvironmentEstimatedHourlyCostUSD.WithLabelValues(env.Name, env.Namespace).Set(hourlyUSD)

	return EnvironmentCostEstimate{
		HourlyUSD:       hourlyUSD,
		AccruedUSD:      accruedUSD,
		ProjectedTTLUSD: projectedTTLUSD,
		IdleDuration:    idleDuration,
		ShouldHibernate: shouldHibernate,
	}
}

// ShouldHibernateIdleEnvironment returns true when the Environment is in Running phase
// and has been idle longer than its configured hibernate-after annotation (or idleThreshold).
func ShouldHibernateIdleEnvironment(env *v1alpha1.Environment, lastTrafficAt time.Time, now time.Time, idleThreshold time.Duration) bool {
	if env == nil || env.Status.Phase != v1alpha1.PhaseRunning {
		return false
	}

	threshold := idleThreshold
	if raw, ok := env.Annotations[AnnotationHibernateAfter]; ok && raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			threshold = parsed
		}
	}
	if threshold <= 0 {
		threshold = defaultIdleThreshold
	}

	refTime := lastTrafficAt
	if refTime.IsZero() {
		refTime = env.CreationTimestamp.Time
	}
	if refTime.IsZero() && env.Status.CreatedAt != nil {
		refTime = env.Status.CreatedAt.Time
	}
	if refTime.IsZero() || !now.After(refTime) {
		return false
	}

	return now.Sub(refTime) >= threshold
}
