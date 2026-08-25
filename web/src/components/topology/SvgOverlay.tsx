import { useEffect, useRef, useState, useCallback } from 'react'
import type { TopologyConnection, ConnectionStatus, TraceEdgeData } from './types'

interface AnchorPoint {
  x: number
  y: number
}

interface SvgConnection {
  id: string
  path: string
  status: ConnectionStatus
  label?: string
  traceData?: TraceEdgeData
  midX: number
  midY: number
}

function getBezierPath(x1: number, y1: number, x2: number, y2: number): string {
  const dx = Math.max(40, (x2 - x1) / 2)
  return `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`
}

export function getEdgeStrokeColor(errorRate: number): string {
  if (errorRate >= 0.05) return '#ef4444'
  if (errorRate >= 0.01) return '#eab308'
  return '#22c55e'
}

export function getEdgeStrokeWidth(requestRate: number): number {
  return Math.min(4, Math.max(1.5, 1.5 + (requestRate / 50)))
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

      const midX = (from.x + to.x) / 2
      const midY = (from.y + to.y) / 2

      paths.push({
        id: conn.id,
        path: getBezierPath(from.x, from.y, to.x, to.y),
        status: conn.status,
        label: conn.label,
        traceData: conn.traceData,
        midX,
        midY,
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

    // Also observe individual topology nodes for size changes (e.g. log snippet expand)
    const nodes = container.querySelectorAll('[data-topology-id]')
    nodes.forEach((node) => resizeObserver.observe(node))

    // Also recalculate on window resize
    window.addEventListener('resize', calculatePaths)

    return () => {
      resizeObserver.disconnect()
      window.removeEventListener('resize', calculatePaths)
    }
  }, [calculatePaths, containerRef])

  if (svgConnections.length === 0) return null

  return (
    <>
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

          let stroke = style.stroke
          let strokeWidth = 1.5

          if (conn.traceData) {
            stroke = getEdgeStrokeColor(conn.traceData.errorRate)
            strokeWidth = getEdgeStrokeWidth(conn.traceData.requestRate)
          }

          return (
            <path
              key={conn.id}
              d={conn.path}
              fill="none"
              stroke={stroke}
              strokeWidth={strokeWidth}
              strokeDasharray={style.dashArray}
              strokeLinecap="round"
              style={style.animate ? { animation: 'dash-flow 1s linear infinite' } : undefined}
              className="transition-[stroke] duration-500"
            />
          )
        })}
      </svg>
      {svgConnections.map((conn) => {
        if (!conn.traceData) return null
        return (
          <div
            key={`${conn.id}-label`}
            className="absolute hidden md:flex z-10"
            style={{ left: conn.midX, top: conn.midY, transform: 'translate(-50%, -50%)' }}
          >
            <div className="bg-background/95 border border-border shadow-sm rounded-md px-2 py-1 text-[10px] flex gap-2.5 items-center">
              <span className="font-mono text-muted-foreground">{Math.round(conn.traceData.requestRate)} req/s</span>
              <span className={conn.traceData.errorRate > 0.01 ? "text-destructive font-mono font-medium" : "font-mono text-muted-foreground"}>
                {(conn.traceData.errorRate * 100).toFixed(1).replace(/\.0$/, '')}%
              </span>
              <span className="font-mono text-muted-foreground">{Math.round(conn.traceData.p99Latency)}ms</span>
            </div>
          </div>
        )
      })}
    </>
  )
}
