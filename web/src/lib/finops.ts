import type { PartialMessage } from '@bufbuild/protobuf'
import type {
  Environment,
  EnvironmentSpec,
  EnvironmentDeploy,
  ResourceOverride,
} from '@/api/gen/diverge/v1alpha1/environment_pb'
import { isSafeExternalUrl } from '@/lib/utils'

export type FinOpsEnvironment = PartialMessage<Environment> & {
  spec?: PartialMessage<EnvironmentSpec> & {
    deploy?: PartialMessage<EnvironmentDeploy> & {
      replicas?: number
      resources?: PartialMessage<ResourceOverride>
    }
  }
}

const CPU_CORE_HOURLY_RATE_USD = 0.031611
const MEMORY_GIB_HOURLY_RATE_USD = 0.004237
export const DEFAULT_POD_HOURLY_COST_USD = 0.012
export const DEFAULT_SCHEMA_DB_HOURLY_USD = 0.01
export const DEFAULT_DEDICATED_DB_HOURLY_USD = 0.08

/**
 * Estimates the hourly database cost in USD based on the database mode/provider.
 */
export function estimateDatabaseHourlyCost(mode?: string): number {
  switch (mode?.toLowerCase()) {
    case 'cnpg':
    case 'neon':
    case 'snapshot':
    case 'fresh':
      return DEFAULT_DEDICATED_DB_HOURLY_USD
    case 'schema':
      return DEFAULT_SCHEMA_DB_HOURLY_USD
    default:
      return 0
  }
}

/**
 * Parses a Kubernetes CPU quantity string (e.g. "500m", "0.5", "2") into cores.
 */
export function parseCpuCores(raw?: string): number {
  if (!raw) return 0
  const trimmed = raw.trim()
  const match = /^(\d+(?:\.\d+)?|\.\d+)(m)?$/.exec(trimmed)
  if (!match) return 0
  const val = Number.parseFloat(match[1])
  if (!Number.isFinite(val) || val <= 0) return 0
  return match[2] === 'm' ? val / 1000 : val
}

/**
 * Parses a Kubernetes memory quantity string (e.g. "512Mi", "1Gi", "2048Ki", "1Ti") into GiB.
 */
export function parseMemoryGiB(raw?: string): number {
  if (!raw) return 0
  const trimmed = raw.trim()
  const match = /^(\d+(?:\.\d+)?|\.\d+)\s*(Ki|Mi|Gi|Ti)?$/.exec(trimmed)
  if (!match) return 0
  const val = Number.parseFloat(match[1])
  if (!Number.isFinite(val) || val <= 0) return 0
  const unit = match[2]
  switch (unit) {
    case 'Ki':
      return val / (1024 * 1024)
    case 'Mi':
      return val / 1024
    case 'Gi':
      return val
    case 'Ti':
      return val * 1024
    default:
      return val / (1024 * 1024 * 1024)
  }
}

/**
 * Returns true when an environment is currently scaled-to-zero / hibernated.
 */
export function isEnvironmentHibernated(env?: FinOpsEnvironment | null): boolean {
  if (!env) return false
  if (env.annotations?.['divergedev.com/hibernated'] === 'true') {
    return true
  }
  return env.status?.phase?.toLowerCase() === 'hibernated'
}

/**
 * Returns the effective preview service count (changedServices or status.services, minimum 1).
 */
export function estimateEnvironmentServiceCount(env?: FinOpsEnvironment | null): number {
  if (!env) return 0
  const changedCount = env.spec?.deploy?.changedServices?.length ?? 0
  const statusCount = env.status?.services?.length ?? 0
  return Math.max(changedCount, statusCount, 1)
}

function computeActiveEnvironmentHourlyComputeUSD(env: FinOpsEnvironment): number {
  const rawReplicas = env.spec?.deploy?.replicas
  const replicas = rawReplicas && rawReplicas > 0 ? rawReplicas : 1
  const serviceCount = estimateEnvironmentServiceCount(env)
  const resources = env.spec?.deploy?.resources ?? env.spec?.serviceConfig?.resources
  const cpuRequest = resources?.cpuRequest?.trim() ?? ''
  const memoryRequest = resources?.memoryRequest?.trim() ?? ''

  let perServiceCost = DEFAULT_POD_HOURLY_COST_USD * replicas
  if (cpuRequest || memoryRequest) {
    const cpuCores = parseCpuCores(cpuRequest)
    const memoryGiB = parseMemoryGiB(memoryRequest)
    if (cpuCores > 0 || memoryGiB > 0) {
      perServiceCost = (cpuCores * CPU_CORE_HOURLY_RATE_USD + memoryGiB * MEMORY_GIB_HOURLY_RATE_USD) * replicas
    }
  }
  return perServiceCost * serviceCount
}

/**
 * Estimates the hourly infrastructure cost in USD for a preview environment.
 */
export function estimateEnvironmentHourlyCostUSD(env?: FinOpsEnvironment | null): number {
  if (!env) return 0
  const phase = env.status?.phase
  if (phase === 'Failed' || phase === 'Terminating' || phase === 'Expired') {
    return 0
  }

  const computeCost = isEnvironmentHibernated(env) ? 0 : computeActiveEnvironmentHourlyComputeUSD(env)
  const dbCost = estimateDatabaseHourlyCost(env.spec?.database?.mode || env.status?.databaseProvider)

  return computeCost + dbCost
}

/**
 * Estimates the hourly compute USD currently saved while an environment is scaled-to-zero / hibernated.
 */
export function estimateEnvironmentHibernationSavingsUSD(env?: FinOpsEnvironment | null): number {
  if (!env) return 0
  const phase = env.status?.phase
  if (phase === 'Failed' || phase === 'Terminating' || phase === 'Expired') {
    return 0
  }
  if (!isEnvironmentHibernated(env)) {
    return 0
  }
  return computeActiveEnvironmentHourlyComputeUSD(env)
}

/**
 * Computes elapsed uptime in hours from environment creation timestamp to `nowMs`.
 */
export function estimateEnvironmentUptimeHours(env?: FinOpsEnvironment | null, nowMs = Date.now()): number {
  if (!env) return 0
  const created = env.createdAt as { toDate?: () => Date; seconds?: bigint | number } | undefined
  let startMs = 0
  if (created?.toDate) {
    startMs = created.toDate().getTime()
  } else if (created?.seconds !== undefined) {
    startMs = Number(created.seconds) * 1000
  }
  if (!Number.isFinite(startMs) || startMs <= 0 || nowMs <= startMs) {
    return 0
  }
  return (nowMs - startMs) / (1000 * 60 * 60)
}

/**
 * Estimates total accrued infrastructure spend in USD over an environment's lifetime.
 */
export function estimateEnvironmentAccruedCostUSD(env?: FinOpsEnvironment | null, nowMs = Date.now()): number {
  const hourly = estimateEnvironmentHourlyCostUSD(env)
  const uptimeHours = estimateEnvironmentUptimeHours(env, nowMs)
  return hourly * uptimeHours
}

/**
 * Formats a cumulative USD value as "$0.00" (or "$0.000" for sub-cent amounts).
 */
export function formatCurrencyUSD(amount: number): string {
  const safe = Number.isFinite(amount) && amount >= 0 ? amount : 0
  if (safe > 0 && safe < 0.01) {
    return `$${safe.toFixed(3)}`
  }
  return `$${safe.toFixed(2)}`
}

/**
 * Formats an hourly USD cost as "$0.000/hr".
 */
export function formatHourlyCostUSD(cost: number): string {
  const safeCost = Number.isFinite(cost) && cost >= 0 ? cost : 0
  return `$${safeCost.toFixed(3)}/hr`
}

/**
 * Builds a shareable zero-extension magic preview link with ?_diverge_env=<env> appended.
 */
export function buildMagicPreviewUrl(env?: FinOpsEnvironment | null): string | undefined {
  const rawUrl = env?.status?.url
  if (!rawUrl || !isSafeExternalUrl(rawUrl)) {
    return undefined
  }
  const envValue = env?.spec?.routing?.headerValue || env?.name || ''
  if (!envValue) {
    return undefined
  }
  try {
    const url = new URL(rawUrl)
    url.searchParams.set('_diverge_env', envValue)
    return url.toString()
  } catch {
    return undefined
  }
}
