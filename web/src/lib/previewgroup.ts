import { Code, ConnectError } from '@connectrpc/connect'
import type { PartialMessage } from '@bufbuild/protobuf'
import type { Environment, EnvironmentDatabase } from '@/api/gen/diverge/v1alpha1/environment_pb'
import type { PreviewGroupServiceSpec } from '@/api/gen/diverge/v1alpha1/previewgroup_pb'

export const GROUP_MEMBERS_ANNOTATION = 'divergedev.com/group-members'
export const COMPOSED_GROUP_LABEL = 'divergedev.com/composed'
export const DEFAULT_ROUTING_HEADER_KEY = 'x-diverge-env'

export interface GroupMemberRecord {
  service: string
  namespace?: string
  provider?: string
  project?: string
  mr?: number
  branch?: string
  environmentName?: string
  environmentNamespace?: string
  environmentResourceVersion?: string
  updatedAt?: string
}

function optTrimmedString(v: unknown): string | undefined {
  return typeof v === 'string' && v.trim().length > 0 ? v.trim() : undefined
}

function optPositiveInt(v: unknown): number | undefined {
  return typeof v === 'number' && Number.isFinite(v) && v > 0 ? Math.trunc(v) : undefined
}

export function parseGroupMembers(annotations?: Record<string, string>): GroupMemberRecord[] {
  const raw = annotations?.[GROUP_MEMBERS_ANNOTATION]?.trim()
  if (!raw) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    const out: GroupMemberRecord[] = []
    for (const item of parsed) {
      if (!item || typeof item !== 'object') continue
      const rec = item as Record<string, unknown>
      const service = optTrimmedString(rec.service)
      if (!service) continue
      out.push({
        service,
        namespace: optTrimmedString(rec.namespace),
        provider: optTrimmedString(rec.provider),
        project: optTrimmedString(rec.project),
        mr: optPositiveInt(rec.mr),
        branch: optTrimmedString(rec.branch),
        environmentName: optTrimmedString(rec.environmentName),
        environmentNamespace: optTrimmedString(rec.environmentNamespace),
        environmentResourceVersion: optTrimmedString(rec.environmentResourceVersion),
        updatedAt: optTrimmedString(rec.updatedAt),
      })
    }
    return out
  } catch {
    return []
  }
}

export function serializeGroupMembers(members: GroupMemberRecord[]): string {
  return JSON.stringify(members)
}

export function countDistinctGroupMRs(members: GroupMemberRecord[]): number {
  const seen = new Set<string>()
  for (const m of members) {
    if (m.mr && m.mr > 0) {
      const prov = (m.provider || 'local').toLowerCase()
      const proj = (m.project || '').toLowerCase()
      seen.add(`${prov}:${proj}#${m.mr}`)
    }
  }
  return seen.size
}

export function sameGroupMemberOwner(a?: GroupMemberRecord, b?: GroupMemberRecord): boolean {
  if (!a || !b) return true
  const aMR = a.mr ?? 0
  const bMR = b.mr ?? 0
  if (aMR > 0 && bMR > 0) {
    const aProv = (a.provider || 'local').toLowerCase()
    const bProv = (b.provider || 'local').toLowerCase()
    const aProj = (a.project || '').toLowerCase()
    const bProj = (b.project || '').toLowerCase()
    return aProv === bProv && aProj === bProj && aMR === bMR
  }
  if (a.environmentName && b.environmentName) {
    const aNs = a.environmentNamespace || a.namespace || 'default'
    const bNs = b.environmentNamespace || b.namespace || 'default'
    return a.environmentName === b.environmentName && aNs === bNs
  }
  const aProv = (a.provider || 'local').toLowerCase()
  const bProv = (b.provider || 'local').toLowerCase()
  if (aProv === 'local' && bProv === 'local' && aMR === 0 && bMR === 0 && !a.environmentName && !b.environmentName) {
    return (a.namespace || 'default') === (b.namespace || 'default')
  }
  return false
}

const SHA256_K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
])

function rotr32(x: number, n: number): number {
  return ((x >>> n) | (x << (32 - n))) >>> 0
}

/**
 * Returns the first 8 lowercase hex characters of SHA-256(utf8(input)), matching Go hex.EncodeToString(sum[:4]).
 */
export function sha256Hex8(input: string): string {
  const bytes = new TextEncoder().encode(input)
  const bitLen = bytes.length * 8
  const paddedLen = Math.ceil((bytes.length + 9) / 64) * 64
  const buf = new Uint8Array(paddedLen)
  buf.set(bytes)
  buf[bytes.length] = 0x80
  const view = new DataView(buf.buffer)
  view.setUint32(paddedLen - 8, Math.floor(bitLen / 0x100000000), false)
  view.setUint32(paddedLen - 4, bitLen >>> 0, false)

  let h0 = 0x6a09e667
  const h = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
    0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
  ])
  const w = new Uint32Array(64)

  for (let offset = 0; offset < paddedLen; offset += 64) {
    for (let i = 0; i < 16; i++) {
      w[i] = view.getUint32(offset + i * 4, false)
    }
    for (let i = 16; i < 64; i++) {
      const s0 = rotr32(w[i - 15], 7) ^ rotr32(w[i - 15], 18) ^ (w[i - 15] >>> 3)
      const s1 = rotr32(w[i - 2], 17) ^ rotr32(w[i - 2], 19) ^ (w[i - 2] >>> 10)
      w[i] = (w[i - 16] + s0 + w[i - 7] + s1) >>> 0
    }
    let a = h[0], b = h[1], c = h[2], d = h[3], e = h[4], f = h[5], g = h[6], hh = h[7]
    for (let i = 0; i < 64; i++) {
      const S1 = rotr32(e, 6) ^ rotr32(e, 11) ^ rotr32(e, 25)
      const ch = (e & f) ^ (~e & g)
      const temp1 = (hh + S1 + ch + SHA256_K[i] + w[i]) >>> 0
      const S0 = rotr32(a, 2) ^ rotr32(a, 13) ^ rotr32(a, 22)
      const maj = (a & b) ^ (a & c) ^ (b & c)
      const temp2 = (S0 + maj) >>> 0
      hh = g
      g = f
      f = e
      e = (d + temp1) >>> 0
      d = c
      c = b
      b = a
      a = (temp1 + temp2) >>> 0
    }
    h[0] = (h[0] + a) >>> 0
    h[1] = (h[1] + b) >>> 0
    h[2] = (h[2] + c) >>> 0
    h[3] = (h[3] + d) >>> 0
    h[4] = (h[4] + e) >>> 0
    h[5] = (h[5] + f) >>> 0
    h[6] = (h[6] + g) >>> 0
    h[7] = (h[7] + hh) >>> 0
  }
  h0 = h[0]
  return h0.toString(16).padStart(8, '0')
}

export function appendPreviewEnvParam(rawUrl: string, headerVal: string): string {
  const trimmed = rawUrl.trim()
  if (!trimmed || !headerVal.trim()) return trimmed
  try {
    const parsed = new URL(trimmed)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return trimmed
    }
    if (!parsed.searchParams.has('_diverge_env')) {
      parsed.searchParams.set('_diverge_env', headerVal.trim())
    }
    return parsed.toString()
  } catch {
    return trimmed
  }
}

export function isAbortedConflictError(err: unknown): boolean {
  if (err instanceof ConnectError) {
    return err.code === Code.Aborted
  }
  return err instanceof Error && /\b(aborted|conflict)\b|has been modified/i.test(err.message)
}

export function normalizeGroupSlug(raw: string): string {
  const trimmed = raw.trim()
  if (!trimmed) return ''
  const slug = trimmed
    .toLowerCase()
    .replace(/[/_.]+/g, '-')
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/-{2,}/g, '-')
    .replace(/^-+|-+$/g, '')
  if (!slug) return ''
  if (slug.length > 63) {
    const suffix = sha256Hex8(trimmed)
    const prefix = slug.slice(0, 63 - 1 - suffix.length).replace(/-+$/g, '') || 'pg'
    return `${prefix}-${suffix}`
  }
  return slug
}

export function previewGroupPath(pg: { name: string; namespace?: string }): string {
  const ns = pg.namespace?.trim()
  return ns ? `/preview-groups/${ns}/${pg.name}` : `/preview-groups/${pg.name}`
}

export function formatGroupMemberBadge(member?: GroupMemberRecord): string {
  if (!member) return ''
  const parts: string[] = []
  if (member.project && member.mr && member.mr > 0) {
    parts.push(`${member.project}#${member.mr}`)
  } else if (member.project) {
    parts.push(member.project)
  } else if (member.mr && member.mr > 0) {
    parts.push(`#${member.mr}`)
  }
  if (member.branch) {
    parts.push(member.branch)
  }
  return parts.join(' · ')
}

function hasDatabaseConfig(db?: EnvironmentDatabase): boolean {
  if (!db) return false
  return Boolean(
    db.mode?.trim() ||
      db.connectionRef?.trim() ||
      db.seedSource?.trim() ||
      db.migrationJob ||
      db.atlas,
  )
}

export interface EnvironmentServiceCandidate {
  key: string
  environmentName: string
  namespace: string
  serviceName: string
  image: string
  port: number
  provider: string
  project: string
  mr: number
  branch: string
  spec: PartialMessage<PreviewGroupServiceSpec>
  member: GroupMemberRecord
}

export function extractEnvironmentCandidate(env: Environment): EnvironmentServiceCandidate | null {
  const sc = env.spec?.serviceConfig
  const image = sc?.image?.trim() || env.annotations?.['divergedev.com/image']?.trim() || ''
  if (!image) {
    return null
  }
  const changedSvc = env.spec?.deploy?.changedServices?.find((s) => s.trim().length > 0)?.trim()
  const serviceName =
    sc?.serviceName?.trim() ||
    changedSvc ||
    env.labels?.['divergedev.com/service']?.trim() ||
    env.name?.trim() ||
    ''
  if (!serviceName) {
    return null
  }

  const namespace = sc?.namespace?.trim() || env.namespace?.trim() || 'default'
  const port = sc?.port ?? 0
  const source = env.spec?.source
  const provider = source?.provider?.trim() || 'local'
  const project = source?.project?.trim() || ''
  const mr = source?.mr ?? 0
  const branch = source?.branch?.trim() || ''

  const spec: PartialMessage<PreviewGroupServiceSpec> = {
    name: serviceName,
    image,
    mode: 'image',
    namespace,
    port,
    parentRef: sc?.parentRef || undefined,
    pathPrefix: sc?.pathPrefix || undefined,
    protocol: sc?.protocol || undefined,
    imagePullPolicy: sc?.imagePullPolicy || undefined,
    env: sc?.env?.length ? sc.env : undefined,
    resources: sc?.resources,
    keda: sc?.keda,
    asyncRoutes: env.spec?.routing?.asyncRoutes?.length ? env.spec.routing.asyncRoutes : undefined,
    database: hasDatabaseConfig(env.spec?.database) ? env.spec?.database : undefined,
    clusterRef: env.spec?.deploy?.clusterRef,
  }

  const member: GroupMemberRecord = {
    service: serviceName,
    namespace,
    provider,
    project: project || undefined,
    mr: mr > 0 ? mr : undefined,
    branch: branch || undefined,
    environmentName: env.name,
    environmentNamespace: env.namespace?.trim() || 'default',
    environmentResourceVersion: env.resourceVersion?.trim() || undefined,
    updatedAt: new Date().toISOString(),
  }

  return {
    key: `${env.namespace || 'default'}/${env.name}`,
    environmentName: env.name,
    namespace,
    serviceName,
    image,
    port,
    provider,
    project,
    mr,
    branch,
    spec,
    member,
  }
}
