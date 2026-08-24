import { Globe, Copy, Cookie } from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import type { IngressNodeData } from './types'

const ROUTING_MODE_COLORS: Record<string, string> = {
  header: 'bg-blue-500/20 text-blue-400 border-blue-500/30',
  subdomain: 'bg-purple-500/20 text-purple-400 border-purple-500/30',
  cookie: 'bg-amber-500/20 text-amber-400 border-amber-500/30',
  namespace: 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30',
}

export function IngressNode({ data }: { data: IngressNodeData }) {
  const modeColor = ROUTING_MODE_COLORS[data.routingMode] ?? ROUTING_MODE_COLORS.header

  return (
    <Card className="border-border/60 bg-card" data-topology-id={data.id}>
      <CardContent className="p-4 space-y-3">
        <div className="flex items-center gap-2">
          <Globe className="h-4 w-4 text-primary" />
          <span className="font-medium text-sm">Ingress</span>
          <span className={`ml-auto text-xs px-2 py-0.5 rounded-full border ${modeColor}`}>
            {data.routingMode}
          </span>
        </div>

        {data.externalUrl && (
          <div className="flex items-center gap-1.5">
            <code className="text-xs text-primary truncate flex-1">{data.externalUrl}</code>
            <Button
              size="icon"
              variant="ghost"
              className="h-6 w-6 shrink-0"
              onClick={() => navigator.clipboard.writeText(data.externalUrl)}
            >
              <Copy className="h-3 w-3" />
            </Button>
          </div>
        )}

        {data.routingMode === 'header' && data.headerKey && (
          <div className="text-xs text-muted-foreground font-mono">
            {data.headerKey}: <span className="text-foreground">{data.headerValue}</span>
          </div>
        )}

        {data.hasCookie && (
          <div className="flex items-center gap-1 text-xs text-amber-400">
            <Cookie className="h-3 w-3" />
            <span>Sticky session</span>
          </div>
        )}

        <div className="text-xs text-muted-foreground">
          Provider: {data.provider === 'istio' ? 'Istio' : 'Gateway API'}
        </div>
      </CardContent>
    </Card>
  )
}
