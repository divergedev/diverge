import { useEffect, useRef, useState, useCallback } from 'react'
import type { TopologyConnection, ConnectionStatus } from './types'

interface AnchorPoint {
  x: number
  y: number
}

interface SvgConnection {
  id: string
  path: string
  status: ConnectionStatus
  label?: string
}

function getBezierPath(x1: number, y1: number, x2: number, y2: number): string {
  const dx = Math.max(40, (x2 - x1) / 2)
  return `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`
}

const STATUS_STYLES: Record<ConnectionStatus, { stroke: string; dashArray?: string; animate?: boolean }> = {
  active: { stroke: 'hsl(var(--primary))' },
  deploying: { stroke: 'hsl(var(--primary) / 0.6)', dashArray: '6 4', animate: true },
  error: { stroke: 'hsl(var(--destructive))' },
  inactive: { stroke: 'hsl(var(--border))' },
}

export function SvgOverlay({
  connections,
  containerRef,
}: {
  connections: TopologyConnection[]
  containerRef: React.RefObject<HTMLDivElement | null>
}) {
  const svgRef = useRef<SVGSVGElement>(null)
  const [svgConnections, setSvgConnections] = useState<SvgConnection[]>([])

  const calculatePaths = useCallback(() => {
    const container = containerRef.current
    if (!container) return

    const containerRect = container.getBoundingClientRect()
    const paths: SvgConnection[] = []

    for (const conn of connections) {
      const fromEl = container.querySelector(`[data-topology-id="${conn.from}"]`)
      const toEl = container.querySelector(`[data-topology-id="${conn.to}"]`)
      if (!fromEl || !toEl) continue

      const fromRect = fromEl.getBoundingClientRect()
      const toRect = toEl.getBoundingClientRect()

      // Right side of source → left side of target
      const from: AnchorPoint = {
        x: fromRect.right - containerRect.left,
        y: fromRect.top + fromRect.height / 2 - containerRect.top,
      }
      const to: AnchorPoint = {
        x: toRect.left - containerRect.left,
        y: toRect.top + toRect.height / 2 - containerRect.top,
      }

      paths.push({
        id: conn.id,
        path: getBezierPath(from.x, from.y, to.x, to.y),
        status: conn.status,
        label: conn.label,
      })
    }

    setSvgConnections(paths)
  }, [connections, containerRef])

  useEffect(() => {
    calculatePaths()

    const container = containerRef.current
    if (!container) return

    const resizeObserver = new ResizeObserver(() => {
      requestAnimationFrame(calculatePaths)
    })
    resizeObserver.observe(container)

    // Also recalculate on window resize
    window.addEventListener('resize', calculatePaths)

    return () => {
      resizeObserver.disconnect()
      window.removeEventListener('resize', calculatePaths)
    }
  }, [calculatePaths, containerRef])

  if (svgConnections.length === 0) return null

  return (
    <svg
      ref={svgRef}
      className="absolute inset-0 w-full h-full pointer-events-none hidden md:block"
      style={{ overflow: 'visible' }}
    >
      <defs>
        <style>{`
          @keyframes dash-flow {
            to { stroke-dashoffset: -20; }
          }
        `}</style>
      </defs>
      {svgConnections.map((conn) => {
        const style = STATUS_STYLES[conn.status] ?? STATUS_STYLES.inactive
        return (
          <path
            key={conn.id}
            d={conn.path}
            fill="none"
            stroke={style.stroke}
            strokeWidth={1.5}
            strokeDasharray={style.dashArray}
            strokeLinecap="round"
            style={style.animate ? { animation: 'dash-flow 1s linear infinite' } : undefined}
            className="transition-[stroke] duration-500"
          />
        )
      })}
    </svg>
  )
}
