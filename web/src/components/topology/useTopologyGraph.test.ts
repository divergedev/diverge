import { describe, it, expect } from 'vitest'
import { buildGraphFromPreviewGroup, buildGraphFromEnvironment } from './useTopologyGraph'
import { PreviewGroup } from '@/api/gen/diverge/v1alpha1/previewgroup_pb'
import { Environment } from '@/api/gen/diverge/v1alpha1/environment_pb'

describe('buildGraphFromPreviewGroup', () => {
  it('builds graph with routing + services + database', () => {
    const pg = new PreviewGroup({
      name: 'test-pg',
      namespace: 'default',
      spec: {
        routing: {
          mode: 'header',
          headerKey: 'x-preview-env',
          headerValue: '42',
          externalUrl: 'https://preview.example.com',
        },
        services: [
          { name: 'auth-svc', image: 'auth:latest', mode: 'image', port: 8080, protocol: 'http' },
          { name: 'api-gw', image: 'api:v2', mode: 'image', port: 3000, protocol: 'grpc' },
        ],
        database: { mode: 'schema' },
      },
      status: {
        phase: 'Running',
        services: [
          { name: 'auth-svc', phase: 'Running', environmentName: 'test-pg-auth-svc', url: 'https://auth.preview.com' },
          { name: 'api-gw', phase: 'Deploying', environmentName: 'test-pg-api-gw' },
        ],
      },
    })

    const graph = buildGraphFromPreviewGroup(pg)

    // Ingress
    expect(graph.ingress).not.toBeNull()
    expect(graph.ingress!.routingMode).toBe('header')
    expect(graph.ingress!.externalUrl).toBe('https://preview.example.com')
    expect(graph.ingress!.headerKey).toBe('x-preview-env')
    expect(graph.ingress!.headerValue).toBe('42')

    // Services
    expect(graph.services).toHaveLength(2)
    expect(graph.services[0].name).toBe('auth-svc')
    expect(graph.services[0].phase).toBe('Running')
    expect(graph.services[0].environmentName).toBe('test-pg-auth-svc')
    expect(graph.services[1].name).toBe('api-gw')
    expect(graph.services[1].phase).toBe('Deploying')

    // Dependencies (database for each service since group-level DB)
    expect(graph.dependencies).toHaveLength(2)
    expect(graph.dependencies[0].kind).toBe('database')
    expect(graph.dependencies[0].detail).toBe('schema')
    expect(graph.dependencies[0].isShared).toBe(false)

    // Connections: ingress→auth, ingress→api, auth→db, api→db
    expect(graph.connections).toHaveLength(4)
  })

  it('builds graph with async routes', () => {
    const pg = new PreviewGroup({
      name: 'async-pg',
      namespace: 'default',
      spec: {
        services: [
          {
            name: 'worker',
            mode: 'image',
            asyncRoutes: [
              { protocol: 'temporal', target: 'order-processing' },
              { protocol: 'kafka', target: 'events-topic' },
            ],
          },
        ],
      },
      status: { services: [{ name: 'worker', phase: 'Running' }] },
    })

    const graph = buildGraphFromPreviewGroup(pg)
    expect(graph.dependencies).toHaveLength(2)
    expect(graph.dependencies[0].kind).toBe('temporal')
    expect(graph.dependencies[0].label).toBe('Temporal Task Queue')
    expect(graph.dependencies[0].detail).toBe('order-processing')
    expect(graph.dependencies[1].kind).toBe('kafka')
    expect(graph.dependencies[1].label).toBe('Kafka Topic')
  })

  it('handles missing optional fields gracefully', () => {
    const pg = new PreviewGroup({ name: 'empty-pg', namespace: 'default' })
    const graph = buildGraphFromPreviewGroup(pg)

    expect(graph.ingress).toBeNull()
    expect(graph.services).toHaveLength(0)
    expect(graph.dependencies).toHaveLength(0)
    expect(graph.connections).toHaveLength(0)
  })

  it('marks shared database with isShared flag', () => {
    const pg = new PreviewGroup({
      name: 'shared-db-pg',
      namespace: 'default',
      spec: {
        services: [{ name: 'app', mode: 'image' }],
        database: { mode: 'shared' },
      },
      status: { services: [{ name: 'app', phase: 'Running' }] },
    })

    const graph = buildGraphFromPreviewGroup(pg)
    expect(graph.dependencies[0].isShared).toBe(true)
  })

  it('derives connection status from service phase', () => {
    const pg = new PreviewGroup({
      name: 'status-pg',
      namespace: 'default',
      spec: {
        routing: { mode: 'header' },
        services: [
          { name: 'ok', mode: 'image' },
          { name: 'failing', mode: 'image' },
          { name: 'starting', mode: 'image' },
        ],
      },
      status: {
        services: [
          { name: 'ok', phase: 'Running' },
          { name: 'failing', phase: 'Failed' },
          { name: 'starting', phase: 'Deploying' },
        ],
      },
    })

    const graph = buildGraphFromPreviewGroup(pg)
    const connStatuses = graph.connections
      .filter((c) => c.from === 'ingress')
      .map((c) => ({ to: c.to, status: c.status }))

    expect(connStatuses).toContainEqual({ to: 'svc-ok', status: 'active' })
    expect(connStatuses).toContainEqual({ to: 'svc-failing', status: 'error' })
    expect(connStatuses).toContainEqual({ to: 'svc-starting', status: 'deploying' })
  })

  it('marks services as changed when changedServices is populated in status', () => {
    const pg = new PreviewGroup({
      name: 'changed-pg',
      namespace: 'default',
      spec: {
        services: [
          { name: 'auth', mode: 'image' },
          { name: 'api', mode: 'image' },
          { name: 'worker', mode: 'baseline' },
        ],
      },
      status: {
        services: [
          { name: 'auth', phase: 'Running', changedServices: ['auth'] },
          { name: 'api', phase: 'Running', changedServices: ['api'] },
          { name: 'worker', phase: 'Running', changedServices: [] },
        ],
      },
    })

    const graph = buildGraphFromPreviewGroup(pg)
    expect(graph.services.find((s) => s.name === 'auth')?.isChanged).toBe(true)
    expect(graph.services.find((s) => s.name === 'api')?.isChanged).toBe(true)
    expect(graph.services.find((s) => s.name === 'worker')?.isChanged).toBe(false)
  })

  it('isChanged is false when changedServices is absent', () => {
    const pg = new PreviewGroup({
      name: 'no-changes',
      namespace: 'default',
      spec: {
        services: [{ name: 'svc', mode: 'image' }],
      },
      status: {
        services: [{ name: 'svc', phase: 'Running' }],
      },
    })

    const graph = buildGraphFromPreviewGroup(pg)
    expect(graph.services[0].isChanged).toBe(false)
  })
})

describe('buildGraphFromEnvironment', () => {
  it('builds graph from serviceConfig', () => {
    const env = new Environment({
      name: 'test-env',
      namespace: 'default',
      spec: {
        routing: {
          mode: 'subdomain',
          externalUrl: 'https://test.preview.dev',
          baseDomain: 'preview.dev',
        },
        serviceConfig: {
          serviceName: 'api',
          port: 8080,
          image: 'api:v1',
          protocol: 'http',
        },
        database: { mode: 'fresh' },
      },
      status: {
        phase: 'Running',
        url: 'https://test.preview.dev',
        databaseStatus: 'Ready',
      },
    })

    const graph = buildGraphFromEnvironment(env)

    expect(graph.ingress).not.toBeNull()
    expect(graph.ingress!.routingMode).toBe('subdomain')
    expect(graph.services).toHaveLength(1)
    expect(graph.services[0].name).toBe('api')
    expect(graph.services[0].mode).toBe('image')
    expect(graph.dependencies).toHaveLength(1)
    expect(graph.dependencies[0].kind).toBe('database')
    expect(graph.dependencies[0].status).toBe('Ready')
    // ingress→api + api→db
    expect(graph.connections).toHaveLength(2)
  })

  it('builds graph from status.services fallback', () => {
    const env = new Environment({
      name: 'fallback-env',
      namespace: 'default',
      status: {
        phase: 'Running',
        services: ['frontend', 'backend'],
      },
    })

    const graph = buildGraphFromEnvironment(env)
    expect(graph.services).toHaveLength(2)
    expect(graph.services[0].name).toBe('frontend')
    expect(graph.services[1].name).toBe('backend')
  })

  it('handles empty environment', () => {
    const env = new Environment({ name: 'empty', namespace: 'default' })
    const graph = buildGraphFromEnvironment(env)

    expect(graph.ingress).toBeNull()
    expect(graph.services).toHaveLength(0)
    expect(graph.dependencies).toHaveLength(0)
    expect(graph.connections).toHaveLength(0)
  })
})
