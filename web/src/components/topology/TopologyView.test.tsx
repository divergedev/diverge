import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import { TopologyView } from './TopologyView'
import { PreviewGroup } from '@/api/gen/diverge/v1alpha1/previewgroup_pb'
import { Environment } from '@/api/gen/diverge/v1alpha1/environment_pb'
import * as useTopologyGraphModule from './useTopologyGraph'

describe('TopologyView', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders trace metrics on edges with traceData', async () => {
    vi.spyOn(useTopologyGraphModule, 'useTopologyGraph').mockReturnValue({
      ingress: {
        id: 'ingress', routingMode: 'header', externalUrl: '', headerKey: '', headerValue: '', baseDomain: '', provider: '', hasCookie: false
      },
      services: [{
        id: 'svc-app', name: 'app', namespace: 'default', mode: 'image',
        image: '', port: 80, protocol: 'http', pathPrefix: '', phase: 'Running',
        reason: '', message: '', lastLogSnippet: '', environmentName: '', url: '',
        isChanged: false
      }],
      dependencies: [],
      connections: [{
        id: 'conn-1',
        from: 'ingress',
        to: 'svc-app',
        fromPort: 'right',
        toPort: 'left',
        status: 'active',
        traceData: { requestRate: 42, errorRate: 0.02, p99Latency: 120 }
      }]
    })

    render(<TopologyView />)

    // We need to wait for useEffect to calculate paths and set svgConnections
    await waitFor(() => {
      expect(screen.getByText('42 req/s')).toBeInTheDocument()
    })
    expect(screen.getByText('2%')).toBeInTheDocument()
    expect(screen.getByText('120ms')).toBeInTheDocument()
    expect(screen.queryByText(/Enable OTel/i)).not.toBeInTheDocument()
  })
  it('renders PreviewGroup with 3 services', () => {
    const pg = new PreviewGroup({
      name: 'test-pg',
      namespace: 'default',
      spec: {
        routing: { mode: 'header', externalUrl: 'https://preview.example.com' },
        services: [
          { name: 'frontend', mode: 'image', port: 80 },
          { name: 'api', mode: 'image', port: 3000 },
          { name: 'worker', mode: 'baseline' },
        ],
      },
      status: {
        services: [
          { name: 'frontend', phase: 'Running' },
          { name: 'api', phase: 'Running' },
          { name: 'worker', phase: 'Running' },
        ],
      },
    })

    render(<TopologyView previewGroup={pg} />)

    // Column headers
    expect(screen.getByText('Ingress & Routing')).toBeInTheDocument()
    expect(screen.getByText('Services')).toBeInTheDocument()
    expect(screen.getByText('Dependencies')).toBeInTheDocument()

    // Service cards
    expect(screen.getByText('frontend')).toBeInTheDocument()
    expect(screen.getByText('api')).toBeInTheDocument()
    expect(screen.getByText('worker')).toBeInTheDocument()

    // Ingress
    expect(screen.getByText('Ingress')).toBeInTheDocument()
  })

  it('renders Environment with single service', () => {
    const env = new Environment({
      name: 'test-env',
      namespace: 'default',
      spec: {
        routing: { mode: 'subdomain', externalUrl: 'https://test.preview.dev' },
        serviceConfig: { serviceName: 'api', port: 8080 },
      },
      status: { phase: 'Running' },
    })

    render(<TopologyView environment={env} />)
    expect(screen.getByText('api')).toBeInTheDocument()
  })

  it('shows skeleton state for empty services', () => {
    const pg = new PreviewGroup({ name: 'empty', namespace: 'default' })
    render(<TopologyView previewGroup={pg} />)
    expect(screen.getByText('Waiting for services to deploy…')).toBeInTheDocument()
  })

  it('shows no dependencies message when none configured', () => {
    const pg = new PreviewGroup({
      name: 'no-deps',
      namespace: 'default',
      spec: {
        services: [{ name: 'app', mode: 'image' }],
      },
      status: { services: [{ name: 'app', phase: 'Running' }] },
    })

    render(<TopologyView previewGroup={pg} />)
    expect(screen.getByText('No dependencies')).toBeInTheDocument()
  })

  it('shows no routing message when ingress not configured', () => {
    const pg = new PreviewGroup({
      name: 'no-routing',
      namespace: 'default',
      spec: {
        services: [{ name: 'app', mode: 'image' }],
      },
      status: { services: [{ name: 'app', phase: 'Running' }] },
    })

    render(<TopologyView previewGroup={pg} />)
    expect(screen.getByText('No routing configured')).toBeInTheDocument()
  })

  it('shows OTel CTA when no trace data present', () => {
    const pg = new PreviewGroup({
      name: 'cta-test',
      namespace: 'default',
      spec: { services: [{ name: 'app', mode: 'image' }] },
      status: { services: [{ name: 'app', phase: 'Running' }] },
    })
    render(<TopologyView previewGroup={pg} />)
    expect(screen.getByText(/Enable OTel to see real-time trace metrics/i)).toBeInTheDocument()
    const docsLink = screen.getByText('View docs')
    expect(docsLink).toHaveAttribute('href', '/docs/guides/otel-setup')
  })
})
