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
  updatedAt?: string
}

export function parseGroupMembers(annotations?: Record<string, string>): GroupMemberRecord[] {
  const raw = annotations?.[GROUP_MEMBERS_ANNOTATION]?.trim()
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((item): item is GroupMemberRecord => Boolean(item && typeof item.service === 'string' && item.service.trim()))
  } catch {
    return []
  }
}

export function serializeGroupMembers(members: GroupMemberRecord[]): string {
  return JSON.stringify(members)
}

export function normalizeGroupSlug(raw: string): string {
  const slug = raw
    .trim()
    .toLowerCase()
    .replace(/[/_.]+/g, '-')
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/-{2,}/g, '-')
    .replace(/^-+|-+$/g, '')
  return slug.slice(0, 63).replace(/-+$/g, '')
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
