import { describe, it, expect } from 'vitest'
import { render, screen } from '@/test/utils'
import { ServiceNode } from './ServiceNode'
import type { ServiceNodeData } from './types'

function makeServiceData(overrides: Partial<ServiceNodeData> = {}): ServiceNodeData {
  return {
    id: 'svc-test',
    name: 'test-service',
    namespace: 'default',
    mode: 'image',
    image: 'test:latest',
    port: 8080,
    protocol: 'http',
    pathPrefix: '',
    phase: 'Running',
    reason: '',
    message: '',
    lastLogSnippet: '',
    environmentName: 'env-test',
    url: '',
    isChanged: false,
    ...overrides,
  }
}

describe('ServiceNode', () => {
  it('renders image mode with correct badge', () => {
    render(<ServiceNode data={makeServiceData({ mode: 'image' })} />)
    expect(screen.getByText('Deployed')).toBeInTheDocument()
    expect(screen.getByText('test-service')).toBeInTheDocument()
    expect(screen.getByText(':8080')).toBeInTheDocument()
  })

  it('renders local mode with proxy indicator', () => {
    render(<ServiceNode data={makeServiceData({ mode: 'local' })} />)
    expect(screen.getByText('Local')).toBeInTheDocument()
    expect(screen.getByText('→ proxied from local machine')).toBeInTheDocument()
  })

  it('renders baseline mode', () => {
    render(<ServiceNode data={makeServiceData({ mode: 'baseline' })} />)
    expect(screen.getByText('Baseline')).toBeInTheDocument()
  })

  it('shows modified badge when isChanged is true', () => {
    render(<ServiceNode data={makeServiceData({ isChanged: true })} />)
    expect(screen.getByText('modified')).toBeInTheDocument()
  })

  it('shows error reason inline when phase is Failed', () => {
    render(<ServiceNode data={makeServiceData({
      phase: 'Failed',
      reason: 'CrashLoopBackOff',
      message: 'Container exited with code 1',
    })} />)
    expect(screen.getByText('CrashLoopBackOff')).toBeInTheDocument()
    expect(screen.getByText('Container exited with code 1')).toBeInTheDocument()
  })

  it('hides lastLogSnippet by default (click-to-reveal)', () => {
    render(<ServiceNode data={makeServiceData({
      phase: 'Failed',
      reason: 'OOMKilled',
      lastLogSnippet: 'panic: runtime error: out of memory',
    })} />)
    expect(screen.getByText('Last log output')).toBeInTheDocument()
    // Snippet should NOT be visible initially
    expect(screen.queryByText('panic: runtime error: out of memory')).not.toBeInTheDocument()
  })

  it('shows path prefix when set', () => {
    render(<ServiceNode data={makeServiceData({ pathPrefix: '/api/payments' })} />)
    expect(screen.getByText('/api/payments')).toBeInTheDocument()
  })

  it('renders preview URL link', () => {
    render(<ServiceNode data={makeServiceData({ url: 'https://preview.example.com' })} />)
    expect(screen.getByText('Open preview')).toBeInTheDocument()
  })

  it('links service name to child environment', () => {
    render(<ServiceNode data={makeServiceData({ environmentName: 'child-env' })} />)
    const link = screen.getByText('test-service').closest('a')
    expect(link).toHaveAttribute('href', '/environments/default/child-env')
  })

  it('renders View Traces link when traceExplorerUrl is set', () => {
    render(<ServiceNode data={makeServiceData({ traceExplorerUrl: 'https://jaeger' })} />)
    expect(screen.getByText('Traces')).toBeInTheDocument()
    expect(screen.getByText('Traces').closest('a')).toHaveAttribute('href', 'https://jaeger')
  })

  it('does not render View Traces when traceExplorerUrl is undefined', () => {
    render(<ServiceNode data={makeServiceData({ traceExplorerUrl: undefined })} />)
    expect(screen.queryByText('Traces')).not.toBeInTheDocument()
  })

  it('shows No Telemetry for uninstrumented services', () => {
    render(<ServiceNode data={makeServiceData({ isInstrumented: false })} />)
    expect(screen.getByText('No Telemetry')).toBeInTheDocument()
    const card = screen.getByText('test-service').closest('.border-dashed')
    expect(card).toBeInTheDocument()
  })

  it('shows instrumented styling by default', () => {
    render(<ServiceNode data={makeServiceData()} />)
    expect(screen.queryByText('No Telemetry')).not.toBeInTheDocument()
    const card = screen.getByText('test-service').closest('.border-dashed')
    expect(card).not.toBeInTheDocument()
  })
})
