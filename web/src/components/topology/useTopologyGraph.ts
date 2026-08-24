import { useMemo } from 'react'
import type { Environment } from '@/api/gen/diverge/v1alpha1/environment_pb'
import type { PreviewGroup } from '@/api/gen/diverge/v1alpha1/previewgroup_pb'
import type {
  TopologyGraph,
  IngressNodeData,
  ServiceNodeData,
  DependencyNodeData,
  TopologyConnection,
  ConnectionStatus,
} from './types'

function phaseToConnectionStatus(phase: string): ConnectionStatus {
  switch (phase) {
    case 'Running':
    case 'Ready':
      return 'active'
    case 'Deploying':
    case 'Provisioning':
    case 'Pending':
      return 'deploying'
    case 'Failed':
    case 'Degraded':
    case 'Error':
      return 'error'
    default:
      return 'inactive'
  }
}

function buildIngressNode(
  routing?: { mode?: string; headerKey?: string; headerValue?: string; externalUrl?: string; baseDomain?: string; provider?: string; cookie?: { enabled?: boolean } },
): IngressNodeData | null {
  if (!routing) return null
  const mode = routing.mode || 'header'
  return {
    id: 'ingress',
    routingMode: mode as IngressNodeData['routingMode'],
    externalUrl: routing.externalUrl || '',
    headerKey: routing.headerKey || 'x-preview-env',
    headerValue: routing.headerValue || '',
    baseDomain: routing.baseDomain || '',
    provider: routing.provider || 'gateway',
    hasCookie: routing.cookie?.enabled ?? false,
  }
}

export function buildGraphFromPreviewGroup(pg: PreviewGroup): TopologyGraph {
  const ingress = buildIngressNode(pg.spec?.routing as Parameters<typeof buildIngressNode>[0])
  // TODO: changedServices lives on child Environment.spec.deploy.changedServices,
  // not on PreviewGroup.spec. Need to surface this via the PreviewGroup API
  // (e.g. add changed_services to PreviewGroupServiceStatus) to populate isChanged.
  const changedServices = new Set<string>()
  const services: ServiceNodeData[] = []
  const dependencies: DependencyNodeData[] = []
  const connections: TopologyConnection[] = []

  // Build service nodes
  const specServices = pg.spec?.services ?? []
  const statusServices = pg.status?.services ?? []

  for (const svc of specServices) {
    const statusMatch = statusServices.find((s) => s.name === svc.name)
    const svcId = `svc-${svc.name}`
    const phase = statusMatch?.phase ?? 'Unknown'

    services.push({
      id: svcId,
      name: svc.name,
      namespace: svc.namespace || pg.namespace,
      mode: (svc.mode || 'image') as ServiceNodeData['mode'],
      image: svc.image,
      port: svc.port,
      protocol: (svc.protocol || 'http') as ServiceNodeData['protocol'],
      pathPrefix: svc.pathPrefix,
      phase,
      reason: statusMatch?.reason ?? '',
      message: statusMatch?.message ?? '',
      lastLogSnippet: statusMatch?.lastLogSnippet ?? '',
      environmentName: statusMatch?.environmentName ?? '',
      url: statusMatch?.url ?? '',
      isChanged: changedServices.has(svc.name),
    })

    // Ingress → Service connection
    if (ingress) {
      connections.push({
        id: `conn-ingress-${svc.name}`,
        from: 'ingress',
        to: svcId,
        fromPort: 'right',
        toPort: 'left',
        status: phaseToConnectionStatus(phase),
        label: svc.pathPrefix || undefined,
      })
    }

    // Service → Database dependency
    const db = svc.database || pg.spec?.database
    if (db) {
      const dbId = `dep-db-${svc.name}`
      dependencies.push({
        id: dbId,
        kind: 'database',
        label: 'PostgreSQL',
        detail: db.mode || 'shared',
        isShared: db.mode === 'shared',
        status: '',
        parentServiceId: svcId,
      })
      connections.push({
        id: `conn-${svc.name}-db`,
        from: svcId,
        to: dbId,
        fromPort: 'right',
        toPort: 'left',
        status: phaseToConnectionStatus(phase),
      })
    }

    // Service → Async route dependencies
    for (const route of svc.asyncRoutes ?? []) {
      const asyncId = `dep-async-${svc.name}-${route.target}`
      const kind = route.protocol === 'kafka' ? 'kafka' : 'temporal'
      dependencies.push({
        id: asyncId,
        kind,
        label: kind === 'kafka' ? 'Kafka Topic' : 'Temporal Task Queue',
        detail: route.target,
        isShared: false,
        status: '',
        parentServiceId: svcId,
      })
      connections.push({
        id: `conn-${svc.name}-${route.target}`,
        from: svcId,
        to: asyncId,
        fromPort: 'right',
        toPort: 'left',
        status: phaseToConnectionStatus(phase),
      })
    }
  }

  return { ingress, services, dependencies, connections }
}

export function buildGraphFromEnvironment(env: Environment): TopologyGraph {
  const ingress = buildIngressNode(env.spec?.routing as Parameters<typeof buildIngressNode>[0])
  const services: ServiceNodeData[] = []
  const dependencies: DependencyNodeData[] = []
  const connections: TopologyConnection[] = []

  const svcConfig = env.spec?.serviceConfig
  const phase = env.status?.phase ?? 'Unknown'

  if (svcConfig) {
    const svcId = `svc-${svcConfig.serviceName}`
    services.push({
      id: svcId,
      name: svcConfig.serviceName,
      namespace: svcConfig.namespace || env.namespace,
      mode: svcConfig.endpoint ? 'local' : 'image',
      image: svcConfig.image,
      port: svcConfig.port,
      protocol: (svcConfig.protocol || 'http') as ServiceNodeData['protocol'],
      pathPrefix: svcConfig.pathPrefix,
      phase,
      reason: '',
      message: '',
      lastLogSnippet: '',
      environmentName: env.name,
      url: env.status?.url ?? '',
      isChanged: true,
    })

    // Ingress → Service
    if (ingress) {
      connections.push({
        id: `conn-ingress-${svcConfig.serviceName}`,
        from: 'ingress',
        to: svcId,
        fromPort: 'right',
        toPort: 'left',
        status: phaseToConnectionStatus(phase),
        label: svcConfig.pathPrefix || undefined,
      })
    }

    // Database dependency
    const db = env.spec?.database
    if (db) {
      const dbId = `dep-db-${svcConfig.serviceName}`
      dependencies.push({
        id: dbId,
        kind: 'database',
        label: 'PostgreSQL',
        detail: db.mode || 'shared',
        isShared: db.mode === 'shared',
        status: env.status?.databaseStatus ?? '',
        parentServiceId: svcId,
      })
      connections.push({
        id: `conn-${svcConfig.serviceName}-db`,
        from: svcId,
        to: dbId,
        fromPort: 'right',
        toPort: 'left',
        status: phaseToConnectionStatus(phase),
      })
    }
  } else if (env.status?.services?.length) {
    // Fallback: environment without serviceConfig but with services list
    for (const svcName of env.status.services) {
      const svcId = `svc-${svcName}`
      services.push({
        id: svcId,
        name: svcName,
        namespace: env.namespace,
        mode: 'image',
        image: '',
        port: 0,
        protocol: 'http',
        pathPrefix: '',
        phase,
        reason: '',
        message: '',
        lastLogSnippet: '',
        environmentName: env.name,
        url: env.status?.url ?? '',
        isChanged: false,
      })
      if (ingress) {
        connections.push({
          id: `conn-ingress-${svcName}`,
          from: 'ingress',
          to: svcId,
          fromPort: 'right',
          toPort: 'left',
          status: phaseToConnectionStatus(phase),
        })
      }
    }
  }

  return { ingress, services, dependencies, connections }
}

export function useTopologyGraph(
  source: { previewGroup?: PreviewGroup; environment?: Environment },
): TopologyGraph {
  return useMemo(() => {
    if (source.previewGroup) {
      return buildGraphFromPreviewGroup(source.previewGroup)
    }
    if (source.environment) {
      return buildGraphFromEnvironment(source.environment)
    }
    return { ingress: null, services: [], dependencies: [], connections: [] }
  }, [source.previewGroup, source.environment])
}
