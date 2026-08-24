import { describe, it, expect } from 'vitest'
import * as fc from 'fast-check'
import { render } from '@/test/utils'
import { TopologyView } from './TopologyView'
import { PreviewGroup } from '@/api/gen/diverge/v1alpha1/previewgroup_pb'
import { buildGraphFromPreviewGroup } from './useTopologyGraph'

describe('TopologyView property tests', () => {
  it('renders without crashing for any number of services (0–20)', () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 0, max: 20 }),
        (serviceCount) => {
          const services = Array.from({ length: serviceCount }, (_, i) => ({
            name: `svc-${i}`,
            mode: 'image' as const,
            port: 8080 + i,
          }))

          const pg = new PreviewGroup({
            name: 'prop-test',
            namespace: 'default',
            spec: { services },
            status: {
              services: services.map((s) => ({ name: s.name, phase: 'Running' })),
            },
          })

          // Should not throw
          const { unmount } = render(<TopologyView previewGroup={pg} />)
          unmount()
        },
      ),
      { numRuns: 10 },
    )
  })

  it('builds valid topology graph for any mode combination', () => {
    const modes = ['image', 'local', 'baseline'] as const
    fc.assert(
      fc.property(
        fc.array(fc.constantFrom(...modes), { minLength: 1, maxLength: 10 }),
        (serviceModes) => {
          const services = serviceModes.map((mode, i) => ({
            name: `svc-${i}`,
            mode,
            port: 8080 + i,
          }))

          const pg = new PreviewGroup({
            name: 'mode-test',
            namespace: 'default',
            spec: {
              routing: { mode: 'header' },
              services,
            },
            status: {
              services: services.map((s) => ({ name: s.name, phase: 'Running' })),
            },
          })

          const graph = buildGraphFromPreviewGroup(pg)

          // Every service should have a corresponding node
          expect(graph.services).toHaveLength(serviceModes.length)

          // Every service should have an ingress connection
          const ingressConns = graph.connections.filter((c) => c.from === 'ingress')
          expect(ingressConns).toHaveLength(serviceModes.length)

          // All connection statuses should be valid
          for (const conn of graph.connections) {
            expect(['active', 'deploying', 'error', 'inactive']).toContain(conn.status)
          }
        },
      ),
      { numRuns: 50 },
    )
  })
})
