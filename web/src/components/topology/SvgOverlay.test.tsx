import { describe, it, expect, vi } from 'vitest'
import { render } from '@/test/utils'
import { SvgOverlay, getEdgeStrokeColor, getEdgeStrokeWidth } from './SvgOverlay'
import type { TopologyConnection } from './types'

describe('SvgOverlay', () => {
  it('renders edge label with trace data', () => {
    const connections: TopologyConnection[] = [{
      id: 'conn-1',
      from: 'node-1',
      to: 'node-2',
      fromPort: 'right',
      toPort: 'left',
      status: 'active',
      traceData: { requestRate: 10, errorRate: 0.001, p99Latency: 50 }
    }]

    // Let's create a container mock
    const container = document.createElement('div')
    const node1 = document.createElement('div')
    node1.setAttribute('data-topology-id', 'node-1')
    const node2 = document.createElement('div')
    node2.setAttribute('data-topology-id', 'node-2')
    container.appendChild(node1)
    container.appendChild(node2)

    // mock getBoundingClientRect
    node1.getBoundingClientRect = () => ({ right: 10, top: 10, height: 20, left: 0, bottom: 30, width: 10 } as DOMRect)
    node2.getBoundingClientRect = () => ({ left: 50, top: 10, height: 20, right: 60, bottom: 30, width: 10 } as DOMRect)
    container.getBoundingClientRect = () => ({ left: 0, top: 0, right: 100, bottom: 100, width: 100, height: 100 } as DOMRect)

    // Need a containerRef wrapper
    const containerRef = { current: container }

    // We need to use vi.spyOn to observe ResizeObserver which jsdom doesn't have
    globalThis.ResizeObserver = class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    }

    const { container: rendered } = render(<SvgOverlay connections={connections} containerRef={containerRef} />)

    // wait for calculatePaths (it runs in useEffect)
    expect(rendered.querySelector('div.bg-background\\/95')).toBeInTheDocument()
    expect(rendered.textContent).toContain('10 req/s')
    expect(rendered.textContent).toContain('0.1%')
    expect(rendered.textContent).toContain('50ms')
  })

  it('colors edge green for low error rate', () => {
    expect(getEdgeStrokeColor(0.009)).toBe('#22c55e')
    expect(getEdgeStrokeColor(0)).toBe('#22c55e')
  })

  it('colors edge red for high error rate', () => {
    expect(getEdgeStrokeColor(0.05)).toBe('#ef4444')
    expect(getEdgeStrokeColor(1.0)).toBe('#ef4444')
  })

  it('colors edge yellow for medium error rate', () => {
    expect(getEdgeStrokeColor(0.01)).toBe('#eab308')
    expect(getEdgeStrokeColor(0.04)).toBe('#eab308')
  })

  it('scales edge width by request rate', () => {
    expect(getEdgeStrokeWidth(0)).toBe(1.5)
    expect(getEdgeStrokeWidth(25)).toBe(2.0)
    expect(getEdgeStrokeWidth(50)).toBe(2.5)
    expect(getEdgeStrokeWidth(125)).toBe(4.0)
    expect(getEdgeStrokeWidth(1000)).toBe(4.0)
  })
})
