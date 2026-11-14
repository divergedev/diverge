package metrics

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/resource"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	AnnotationHibernateAfter   = "divergedev.com/hibernate-after"
	AnnotationLastTrafficAt    = "divergedev.com/last-traffic-at"
	AnnotationHibernated       = "divergedev.com/hibernated"
	AnnotationDatabaseProvider = "divergedev.com/database-provider"

	EnvCostVCPUHourUSD        = "DIVERGE_COST_VCPU_HOUR_USD"
	EnvCostMemoryGiBHourUSD   = "DIVERGE_COST_MEMORY_GIB_HOUR_USD"
	EnvCostDBSchemaHourUSD    = "DIVERGE_COST_DB_SCHEMA_HOUR_USD"
	EnvCostDBDedicatedHourUSD = "DIVERGE_COST_DB_DEDICATED_HOUR_USD"

	DefaultServiceCPUCores  = 0.25
	DefaultServiceMemoryGiB = 0.5
	defaultIdleThreshold    = 30 * time.Minute
	defaultPreviewTTL       = 24 * time.Hour
)

var (
	// EnvironmentEstimatedHourlyCostUSD tracks the estimated hourly USD cost per preview environment.
	EnvironmentEstimatedHourlyCostUSD = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "diverge_environment_estimated_hourly_cost_usd",
			Help: "Estimated hourly cost in USD for each preview environment",
		},
		[]string{"environment", "namespace"},
	)

	// EnvironmentHibernationSavingsHourlyUSD tracks hourly compute USD saved while an environment is hibernated.
	EnvironmentHibernationSavingsHourlyUSD = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "diverge_environment_hibernation_savings_hourly_usd",
			Help: "Estimated hourly compute cost in USD saved while the preview environment is hibernated",
		},
		[]string{"environment", "namespace"},
	)

	// EnvironmentCPUCoresRequested tracks active CPU core requests attributed to each preview environment.
	EnvironmentCPUCoresRequested = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "diverge_environment_cpu_cores_requested",
			Help: "Total CPU cores currently requested by active preview services for each environment",
		},
		[]string{"environment", "namespace"},
	)

	// EnvironmentMemoryGiBRequested tracks active memory GiB requests attributed to each preview environment.
	EnvironmentMemoryGiBRequested = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "diverge_environment_memory_gib_requested",
			Help: "Total memory in GiB currently requested by active preview services for each environment",
		},
		[]string{"environment", "namespace"},
	)

	// EnvironmentLifetimeSeconds observes total preview environment lifetimes upon teardown.
	EnvironmentLifetimeSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "diverge_environment_lifetime_seconds",
			Help:    "Total lifetime in seconds of preview environments observed upon teardown",
			Buckets: []float64{60, 300, 900, 1800, 3600, 7200, 14400, 28800, 86400, 172800, 604800},
		},
		[]string{"namespace", "database_mode"},
	)
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		EnvironmentEstimatedHourlyCostUSD,
		EnvironmentHibernationSavingsHourlyUSD,
		EnvironmentCPUCoresRequested,
		EnvironmentMemoryGiBRequested,
		EnvironmentLifetimeSeconds,
	)
}

// DeleteEnvironmentCostMetric removes all per-environment FinOps and resource gauge series for a torn-down environment.
func DeleteEnvironmentCostMetric(name, namespace string) {
	EnvironmentEstimatedHourlyCostUSD.DeleteLabelValues(name, namespace)
	EnvironmentHibernationSavingsHourlyUSD.DeleteLabelValues(name, namespace)
	EnvironmentCPUCoresRequested.DeleteLabelValues(name, namespace)
	EnvironmentMemoryGiBRequested.DeleteLabelValues(name, namespace)
}

// CostRateCard defines the unit hourly resource pricing for preview environments.
type CostRateCard struct {
	VCPUHourUSD            float64
	MemoryGiBHourUSD       float64
	DatabaseSchemaHrUSD    float64
	DatabaseDedicatedHrUSD float64
}

// DefaultCostRateCard returns the standard unit rate card ($0.04/vCPU-hr, $0.004/GiB-hr, $0.01/schema-hr, $0.08/dedicated-db-hr).
func DefaultCostRateCard() CostRateCard {
	return CostRateCard{
		VCPUHourUSD:            0.04,
		MemoryGiBHourUSD:       0.004,
		DatabaseSchemaHrUSD:    0.01,
		DatabaseDedicatedHrUSD: 0.08,
	}
}

// CostRateCardFromEnv loads unit pricing from DIVERGE_COST_* environment variables,
// falling back to DefaultCostRateCard values for unset or invalid entries.
func CostRateCardFromEnv() CostRateCard {
	card := DefaultCostRateCard()
	if v, ok := parseNonNegativeEnvFloat(EnvCostVCPUHourUSD); ok {
		card.VCPUHourUSD = v
	}
	if v, ok := parseNonNegativeEnvFloat(EnvCostMemoryGiBHourUSD); ok {
		card.MemoryGiBHourUSD = v
	}
	if v, ok := parseNonNegativeEnvFloat(EnvCostDBSchemaHourUSD); ok {
		card.DatabaseSchemaHrUSD = v
	}
	if v, ok := parseNonNegativeEnvFloat(EnvCostDBDedicatedHourUSD); ok {
		card.DatabaseDedicatedHrUSD = v
	}
	return card
}

func parseNonNegativeEnvFloat(key string) (float64, bool) {
	raw := strings.TrimPrefix(strings.TrimSpace(os.Getenv(key)), "$")
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

func normalizeRateCard(rate CostRateCard) CostRateCard {
	defaults := DefaultCostRateCard()
	if rate.VCPUHourUSD == 0 && rate.MemoryGiBHourUSD == 0 && rate.DatabaseSchemaHrUSD == 0 && rate.DatabaseDedicatedHrUSD == 0 {
		return defaults
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
	if rate.DatabaseDedicatedHrUSD < 0 {
		rate.DatabaseDedicatedHrUSD = 0
	} else if rate.DatabaseDedicatedHrUSD == 0 && (rate.VCPUHourUSD > 0 || rate.MemoryGiBHourUSD > 0 || rate.DatabaseSchemaHrUSD > 0) {
		rate.DatabaseDedicatedHrUSD = defaults.DatabaseDedicatedHrUSD
	}
	return rate
}

// ResolveEnvironmentDatabaseMode returns the normalized database isolation mode for an Environment.
func ResolveEnvironmentDatabaseMode(env *v1alpha1.Environment) string {
	if env == nil {
		return "none"
	}
	for _, candidate := range []string{
		env.Spec.Database.Mode,
		env.Annotations[AnnotationDatabaseProvider],
		env.Status.DatabaseProvider,
	} {
		mode := strings.ToLower(strings.TrimSpace(candidate))
		if mode != "" {
			if mode == "noop" {
				return "none"
			}
			return mode
		}
	}
	return "none"
}

// ObserveEnvironmentLifetime records the elapsed lifetime of an Environment upon teardown.
func ObserveEnvironmentLifetime(env *v1alpha1.Environment, now time.Time) {
	if env == nil {
		return
	}
	startTime := env.CreationTimestamp.Time
	if startTime.IsZero() && env.Status.CreatedAt != nil {
		startTime = env.Status.CreatedAt.Time
	}
	if startTime.IsZero() || !now.After(startTime) {
		return
	}
	ns := strings.TrimSpace(env.Namespace)
	if ns == "" {
		ns = "default"
	}
	dbMode := ResolveEnvironmentDatabaseMode(env)
	EnvironmentLifetimeSeconds.WithLabelValues(ns, dbMode).Observe(now.Sub(startTime).Seconds())
}

// EnvironmentCostEstimate holds the computed cost, resource footprint, and idle-hibernation recommendation for an Environment.
type EnvironmentCostEstimate struct {
	HourlyUSD                   float64
	ComputeHourlyUSD            float64
	DatabaseHourlyUSD           float64
	HibernationSavingsHourlyUSD float64
	AccruedUSD                  float64
	ProjectedTTLUSD             float64
	UptimeHours                 float64
	CPUCores                    float64
	MemoryGiB                   float64
	ServiceCount                int
	Hibernated                  bool
	IdleDuration                time.Duration
	ShouldHibernate             bool
}

// ComputeEnvironmentCost is the pure, side-effect-free cost calculation for a preview Environment.
// Unlike EstimateEnvironmentCost, it does not mutate global Prometheus gauges, making it safe for read-only API queries.
func ComputeEnvironmentCost(env *v1alpha1.Environment, now time.Time, rate CostRateCard) EnvironmentCostEstimate {
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

	cpuCores := DefaultServiceCPUCores
	memoryGiB := DefaultServiceMemoryGiB
	if env.Spec.ServiceConfig != nil && env.Spec.ServiceConfig.Resources != nil {
		if env.Spec.ServiceConfig.Resources.CPURequest != "" {
			if q, err := resource.ParseQuantity(env.Spec.ServiceConfig.Resources.CPURequest); err == nil {
				if cores := q.AsApproximateFloat64(); cores > 0 {
					cpuCores = cores
				}
			}
		}
		if env.Spec.ServiceConfig.Resources.MemoryRequest != "" {
			if q, err := resource.ParseQuantity(env.Spec.ServiceConfig.Resources.MemoryRequest); err == nil {
				if gib := float64(q.Value()) / (1024 * 1024 * 1024); gib > 0 {
					memoryGiB = gib
				}
			}
		}
	}

	// Default per-service footprint: 0.25 vCPU and 0.5 GiB RAM (overridden by ServiceConfig.Resources)
	perServiceHourly := cpuCores*rate.VCPUHourUSD + memoryGiB*rate.MemoryGiBHourUSD
	baseComputeHourly := float64(serviceCount) * perServiceHourly

	var dbHourly float64
	switch ResolveEnvironmentDatabaseMode(env) {
	case "", "none", "shared":
		dbHourly = 0
	case "cnpg", "neon", "snapshot", "fresh":
		dbHourly = rate.DatabaseDedicatedHrUSD
	default:
		dbHourly = rate.DatabaseSchemaHrUSD
	}

	hibernated := env.Annotations[AnnotationHibernated] == "true" ||
		strings.EqualFold(string(env.Status.Phase), "hibernated")
	terminal := env.Status.Phase == v1alpha1.PhaseFailed ||
		env.Status.Phase == v1alpha1.PhaseTerminating ||
		string(env.Status.Phase) == "Expired"

	var hourlyUSD, computeHourlyUSD, databaseHourlyUSD, hibernationSavingsUSD, activeCPU, activeMemory float64
	switch {
	case terminal:
		// Terminal environments consume no active preview resources.
	case hibernated:
		if baseComputeHourly > 0 {
			hibernationSavingsUSD = baseComputeHourly
		}
	default:
		computeHourlyUSD = baseComputeHourly
		databaseHourlyUSD = dbHourly
		hourlyUSD = computeHourlyUSD + databaseHourlyUSD
		if hourlyUSD < 0 {
			hourlyUSD = 0
		}
		activeCPU = float64(serviceCount) * cpuCores
		activeMemory = float64(serviceCount) * memoryGiB
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

	return EnvironmentCostEstimate{
		HourlyUSD:                   hourlyUSD,
		ComputeHourlyUSD:            computeHourlyUSD,
		DatabaseHourlyUSD:           databaseHourlyUSD,
		HibernationSavingsHourlyUSD: hibernationSavingsUSD,
		AccruedUSD:                  accruedUSD,
		ProjectedTTLUSD:             projectedTTLUSD,
		UptimeHours:                 runtimeHours,
		CPUCores:                    activeCPU,
		MemoryGiB:                   activeMemory,
		ServiceCount:                serviceCount,
		Hibernated:                  hibernated,
		IdleDuration:                idleDuration,
		ShouldHibernate:             shouldHibernate,
	}
}

// EstimateEnvironmentCost computes the hourly, accrued, and projected TTL cost for a preview Environment
// and publishes the per-environment Prometheus gauges.
func EstimateEnvironmentCost(env *v1alpha1.Environment, now time.Time, rate CostRateCard) EnvironmentCostEstimate {
	if env == nil {
		return EnvironmentCostEstimate{}
	}
	est := ComputeEnvironmentCost(env, now, rate)
	EnvironmentEstimatedHourlyCostUSD.WithLabelValues(env.Name, env.Namespace).Set(est.HourlyUSD)
	EnvironmentHibernationSavingsHourlyUSD.WithLabelValues(env.Name, env.Namespace).Set(est.HibernationSavingsHourlyUSD)
	EnvironmentCPUCoresRequested.WithLabelValues(env.Name, env.Namespace).Set(est.CPUCores)
	EnvironmentMemoryGiBRequested.WithLabelValues(env.Name, env.Namespace).Set(est.MemoryGiB)
	return est
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
		if raw, ok := env.Annotations[AnnotationLastTrafficAt]; ok && raw != "" {
			if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
				refTime = parsed
			}
		}
	}
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
