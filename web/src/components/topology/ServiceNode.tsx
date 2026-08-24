import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Server, ChevronDown, ChevronRight, ExternalLink } from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { StatusBadge } from '@/components/StatusBadge'
import type { ServiceNodeData } from './types'
import { MODE_LABELS } from './types'
import { cn } from '@/lib/utils'

const MODE_COLORS: Record<string, string> = {
  image: 'bg-blue-500/20 text-blue-400 border-blue-500/30',
  local: 'bg-amber-500/20 text-amber-400 border-amber-500/30',
  baseline: 'bg-gray-500/20 text-gray-400 border-gray-500/30',
}

export function ServiceNode({ data }: { data: ServiceNodeData }) {
  const [showSnippet, setShowSnippet] = useState(false)
  const modeInfo = MODE_LABELS[data.mode] ?? MODE_LABELS.image
  const modeColor = MODE_COLORS[data.mode] ?? MODE_COLORS.image
  const hasError = data.phase === 'Failed' || data.phase === 'Degraded' || data.phase === 'Error'

  return (
    <Card
      className={cn(
        'border-border/60 bg-card transition-colors',
        data.isChanged && 'border-primary/50 ring-1 ring-primary/20',
        hasError && 'border-destructive/50',
      )}
      data-topology-id={data.id}
    >
      <CardContent className="p-4 space-y-2.5">
        {/* Header: name + status */}
        <div className="flex items-center gap-2">
          <Server className="h-4 w-4 text-muted-foreground" />
          {data.environmentName ? (
            <Link
              to={`/environments/${data.namespace}/${data.environmentName}`}
              className="font-medium text-sm hover:text-primary transition-colors truncate"
            >
              {data.name}
            </Link>
          ) : (
            <span className="font-medium text-sm truncate">{data.name}</span>
          )}
          <div className="ml-auto shrink-0">
            <StatusBadge phase={data.phase} />
          </div>
        </div>

        {/* Mode + protocol badges */}
        <div className="flex items-center gap-1.5 flex-wrap">
          <span
            className={`text-xs px-2 py-0.5 rounded-full border cursor-help ${modeColor}`}
            title={modeInfo.description}
          >
            {modeInfo.label}
          </span>
          {data.port > 0 && (
            <span className="text-xs text-muted-foreground font-mono">
              :{data.port}
            </span>
          )}
          <span className="text-xs text-muted-foreground uppercase">{data.protocol}</span>
          {data.isChanged && (
            <span className="text-xs px-2 py-0.5 rounded-full border bg-primary/20 text-primary border-primary/30">
              modified
            </span>
          )}
        </div>

        {/* Image (for deployed mode) */}
        {data.mode === 'image' && data.image && (
          <div className="text-xs text-muted-foreground font-mono truncate" title={data.image}>
            {data.image.length > 50 ? `…${data.image.slice(-47)}` : data.image}
          </div>
        )}

        {/* Local endpoint */}
        {data.mode === 'local' && (
          <div className="text-xs text-amber-400 font-mono">
            → proxied from local machine
          </div>
        )}

        {/* Path prefix */}
        {data.pathPrefix && (
          <div className="text-xs text-muted-foreground">
            Path: <code className="text-foreground">{data.pathPrefix}</code>
          </div>
        )}

        {/* Preview URL */}
        {data.url && (
          <a
            href={data.url}
            target="_blank"
            rel="noreferrer"
            className="flex items-center gap-1 text-xs text-primary hover:underline"
          >
            <ExternalLink className="h-3 w-3" />
            Open preview
          </a>
        )}

        {/* Error section */}
        {hasError && data.reason && (
          <div className="space-y-1">
            <div className="text-xs text-destructive font-medium">{data.reason}</div>
            {data.message && (
              <div className="text-xs text-muted-foreground">{data.message}</div>
            )}
            {data.lastLogSnippet && (
              <div>
                <button
                  onClick={() => setShowSnippet(!showSnippet)}
                  className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
                >
                  {showSnippet ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
                  Last log output
                </button>
                {showSnippet && (
                  <pre className="mt-1 text-xs font-mono bg-black/50 rounded p-2 text-gray-300 overflow-x-auto max-h-24 overflow-y-auto">
                    {data.lastLogSnippet}
                  </pre>
                )}
              </div>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
