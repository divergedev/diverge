import { Database, Workflow, AlertTriangle } from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import type { DependencyNodeData } from './types'
import { DB_MODE_LABELS } from './types'

const KIND_CONFIG: Record<string, { icon: typeof Database; color: string }> = {
  database: { icon: Database, color: 'text-emerald-400' },
  temporal: { icon: Workflow, color: 'text-violet-400' },
  kafka: { icon: Workflow, color: 'text-orange-400' },
}

export function DependencyNode({ data }: { data: DependencyNodeData }) {
  const config = KIND_CONFIG[data.kind] ?? KIND_CONFIG.database
  const Icon = config.icon
  const dbMode = data.kind === 'database' ? DB_MODE_LABELS[data.detail] : null

  return (
    <Card className="border-border/60 bg-card" data-topology-id={data.id}>
      <CardContent className="p-4 space-y-2.5">
        <div className="flex items-center gap-2">
          <Icon className={`h-4 w-4 ${config.color}`} />
          <span className="font-medium text-sm">{data.label}</span>
          {data.isShared && (
            <span title="Using shared/production database" className="ml-auto shrink-0">
              <AlertTriangle className="h-3.5 w-3.5 text-amber-400" />
            </span>
          )}
        </div>

        {/* Database mode */}
        {dbMode && (
          <div className="flex items-center gap-1.5">
            <span className={`text-xs px-2 py-0.5 rounded-full border ${
              dbMode.warning
                ? 'bg-amber-500/20 text-amber-400 border-amber-500/30'
                : 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30'
            }`}>
              {dbMode.label}
            </span>
          </div>
        )}

        {/* Async route target */}
        {(data.kind === 'temporal' || data.kind === 'kafka') && data.detail && (
          <div className="text-xs font-mono text-muted-foreground truncate" title={data.detail}>
            {data.detail}
          </div>
        )}

        {/* Database status */}
        {data.kind === 'database' && data.status && (
          <div className="text-xs text-muted-foreground">
            Status: <span className="text-foreground">{data.status}</span>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
