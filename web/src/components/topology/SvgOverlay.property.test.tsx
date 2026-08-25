import fc from 'fast-check'
import { describe, it, expect } from 'vitest'
import { getEdgeStrokeColor, getEdgeStrokeWidth } from './SvgOverlay'

describe('SvgOverlay edge metrics PBT', () => {
  it('edge color is always one of green/yellow/red for any error rate', () => {
    fc.assert(
      fc.property(fc.float({ min: 0, max: 1, noNaN: true }), (errorRate) => {
        const color = getEdgeStrokeColor(errorRate)
        expect(['#22c55e', '#eab308', '#ef4444']).toContain(color)

        if (errorRate >= 0.05) {
          expect(color).toBe('#ef4444')
        } else if (errorRate >= 0.01) {
          expect(color).toBe('#eab308')
        } else {
          expect(color).toBe('#22c55e')
        }
      })
    )
  })

  it('edge width is bounded for any request rate', () => {
    fc.assert(
      fc.property(fc.float({ min: 0, max: 10000, noNaN: true }), (requestRate) => {
        const width = getEdgeStrokeWidth(requestRate)
        expect(width).toBeGreaterThanOrEqual(1.5)
        expect(width).toBeLessThanOrEqual(4.0)
      })
    )
  })
})
