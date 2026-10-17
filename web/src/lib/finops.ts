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
 * Estimates the hourly infrastructure cost in USD for a preview environment.
 */
export function estimateEnvironmentHourlyCostUSD(env?: FinOpsEnvironment | null): number {
  if (!env) return 0
  const phase = env.status?.phase
  if (phase === 'Failed' || phase === 'Terminating') {
    return 0
  }

  let computeCost = 0
  if (!isEnvironmentHibernated(env)) {
    const rawReplicas = env.spec?.deploy?.replicas
    const replicas = rawReplicas && rawReplicas > 0 ? rawReplicas : 1
    const resources = env.spec?.deploy?.resources ?? env.spec?.serviceConfig?.resources
    const cpuRequest = resources?.cpuRequest?.trim() ?? ''
    const memoryRequest = resources?.memoryRequest?.trim() ?? ''

    if (cpuRequest || memoryRequest) {
      const cpuCores = parseCpuCores(cpuRequest)
      const memoryGiB = parseMemoryGiB(memoryRequest)
      if (cpuCores > 0 || memoryGiB > 0) {
        computeCost = (cpuCores * CPU_CORE_HOURLY_RATE_USD + memoryGiB * MEMORY_GIB_HOURLY_RATE_USD) * replicas
      } else {
        computeCost = DEFAULT_POD_HOURLY_COST_USD * replicas
      }
    } else {
      computeCost = DEFAULT_POD_HOURLY_COST_USD * replicas
    }
  }

  const dbCost = estimateDatabaseHourlyCost(env.spec?.database?.mode || env.status?.databaseProvider)

  return computeCost + dbCost
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
