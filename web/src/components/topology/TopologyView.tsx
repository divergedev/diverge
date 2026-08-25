import { useRef } from 'react'
import type { Environment } from '@/api/gen/diverge/v1alpha1/environment_pb'
import type { PreviewGroup } from '@/api/gen/diverge/v1alpha1/previewgroup_pb'
import { useTopologyGraph } from './useTopologyGraph'
import { IngressNode } from './IngressNode'
import { ServiceNode } from './ServiceNode'
import { DependencyNode } from './DependencyNode'
import { TopologyColumn } from './TopologyColumn'
import { SvgOverlay } from './SvgOverlay'
import { cn } from '@/lib/utils'

interface TopologyViewProps {
  previewGroup?: PreviewGroup
  environment?: Environment
  className?: string
}

function SkeletonNode() {
  return (
    <div className="rounded-lg border border-border/40 bg-card/50 p-4 space-y-2 animate-pulse">
      <div className="h-4 w-24 bg-muted rounded" />
      <div className="h-3 w-32 bg-muted/60 rounded" />
      <div className="h-3 w-20 bg-muted/40 rounded" />
    </div>
  )
}

export function TopologyView({ previewGroup, environment, className }: TopologyViewProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const graph = useTopologyGraph({ previewGroup, environment })

  const isEmpty = graph.services.length === 0 && !graph.ingress

  if (isEmpty) {
    return (
      <div className={cn('relative', className)}>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-8 p-4">
          <TopologyColumn title="Ingress & Routing">
            <SkeletonNode />
          </TopologyColumn>
          <TopologyColumn title="Services">
            <SkeletonNode />
            <SkeletonNode />
          </TopologyColumn>
          <TopologyColumn title="Dependencies">
            <SkeletonNode />
          </TopologyColumn>
        </div>
        <div className="absolute inset-0 flex items-center justify-center">
          <div className="text-sm text-muted-foreground bg-background/80 px-4 py-2 rounded-md">
            Waiting for services to deploy…
          </div>
        </div>
      </div>
    )
  }

  return (
    <div ref={containerRef} className={cn('relative', className)}>
      <div className="grid grid-cols-1 md:grid-cols-3 gap-8 p-4">
        {/* Column 1: Ingress */}
        <TopologyColumn title="Ingress & Routing">
          {graph.ingress ? (
            <IngressNode data={graph.ingress} />
          ) : (
            <div className="text-xs text-muted-foreground p-4 border border-dashed border-border rounded-lg text-center">
              No routing configured
            </div>
          )}
        </TopologyColumn>

        {/* Column 2: Services */}
        <TopologyColumn title="Services">
          {graph.services.map((svc) => (
            <ServiceNode key={svc.id} data={svc} />
          ))}
        </TopologyColumn>

        {/* Column 3: Dependencies */}
        <TopologyColumn title="Dependencies">
          {graph.dependencies.length > 0 ? (
            graph.dependencies.map((dep) => (
              <DependencyNode key={dep.id} data={dep} />
            ))
          ) : (
            <div className="text-xs text-muted-foreground p-4 border border-dashed border-border rounded-lg text-center">
              No dependencies
            </div>
          )}
        </TopologyColumn>
      </div>

      {/* Trace empty state banner */}
      {graph.services.length > 0 && !graph.connections.some(c => c.traceData) && (
        <div className="absolute top-4 right-4 bg-muted/50 border border-border rounded-md px-3 py-2 text-xs text-muted-foreground shadow-sm">
          Enable OTel to see real-time trace metrics.{' '}
          <a href="#" className="text-primary hover:underline">View docs</a>
        </div>
      )}

      {/* SVG connection lines (hidden on mobile) */}
      <SvgOverlay connections={graph.connections} containerRef={containerRef} />
    </div>
  )
}
