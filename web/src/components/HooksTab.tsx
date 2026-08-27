import { useListHookJobs, useRetryHook } from '@/api/queries'
import { StatusBadge } from '@/components/StatusBadge'
import { LogViewer } from '@/components/LogViewer'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { RotateCw, AlertCircle, CheckCircle2, Clock, Play, ChevronDown, ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useState, Fragment } from 'react'

interface HooksTabProps {
  namespace: string
  environmentName: string
}

function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return s > 0 ? `${m}m ${s}s` : `${m}m`
}

function hookIcon(phase: string) {
  switch (phase) {
    case 'Succeeded': return <CheckCircle2 className="h-4 w-4 text-green-500" />
    case 'Failed': return <AlertCircle className="h-4 w-4 text-destructive" />
    case 'Running': return <Play className="h-4 w-4 text-blue-500 animate-pulse" />
    default: return <Clock className="h-4 w-4 text-muted-foreground" />
  }
}

export function HooksTab({ namespace, environmentName }: HooksTabProps) {
  const { data, isLoading, error } = useListHookJobs(namespace, environmentName)
  const retryHook = useRetryHook()
  const [expandedHook, setExpandedHook] = useState<string | null>(null)

  if (isLoading) {
    return (
      <div className="flex justify-center py-12">
        <div className="animate-spin rounded-full h-6 w-6 border-b-2 border-primary" />
      </div>
    )
  }

  if (error) {
    return (
      <div className="text-center py-12 space-y-2" role="alert">
        <AlertCircle className="h-8 w-8 text-destructive mx-auto" />
        <p className="text-sm text-muted-foreground">{error.message}</p>
      </div>
    )
  }

  const jobs = data?.jobs ?? []

  if (jobs.length === 0) {
    return (
      <Card>
        <CardContent className="py-12 text-center">
          <p className="text-muted-foreground">No hooks configured for this environment.</p>
        </CardContent>
      </Card>
    )
  }

  const handleRetry = (hookType: string) => {
    retryHook.mutate({ namespace, environmentName, hookType })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Hook Jobs</CardTitle>
      </CardHeader>
      <CardContent>
        {retryHook.error && (
          <div className="bg-destructive/10 border border-destructive/20 text-destructive px-4 py-3 rounded-md flex items-center gap-2 text-sm mb-4" role="alert">
            <AlertCircle className="h-4 w-4 flex-shrink-0" />
            Retry failed: {retryHook.error instanceof Error ? retryHook.error.message : 'Unknown error'}
          </div>
        )}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8"></TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Name</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Duration</TableHead>
              <TableHead>Message</TableHead>
              <TableHead className="w-20"></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {jobs.map((job) => {
              const isExpanded = expandedHook === job.name
              return (
                <Fragment key={job.name}>
                  <TableRow
                    key={job.name}
                    className={cn(
                      job.phase === 'Failed' && 'bg-destructive/5',
                      'cursor-pointer hover:bg-muted/50',
                    )}
                    onClick={() => setExpandedHook(isExpanded ? null : job.name)}
                  >
                    <TableCell>
                      {isExpanded
                        ? <ChevronDown className="h-4 w-4 text-muted-foreground" />
                        : <ChevronRight className="h-4 w-4 text-muted-foreground" />}
                    </TableCell>
                    <TableCell className="font-mono text-xs">{job.type}</TableCell>
                    <TableCell className="font-mono text-xs max-w-[200px] truncate">{job.name}</TableCell>
                    <TableCell><StatusBadge phase={job.phase} /></TableCell>
                    <TableCell className="text-sm tabular-nums">
                      {job.durationSeconds > 0 ? formatDuration(job.durationSeconds) : '—'}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground max-w-[300px] truncate" title={job.message}>
                      {job.message || '—'}
                    </TableCell>
                    <TableCell>
                      {job.phase === 'Failed' && (
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={(e) => { e.stopPropagation(); handleRetry(job.type) }}
                          disabled={retryHook.isPending}
                          aria-label={`Retry ${job.type} hook`}
                        >
                          <RotateCw className={cn('h-4 w-4', retryHook.isPending && 'animate-spin')} />
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                  {isExpanded && (
                    <TableRow key={`${job.name}-logs`}>
                      <TableCell colSpan={7} className="p-0">
                        <div className="border-t border-border bg-muted/30 p-2">
                          <p className="text-xs text-muted-foreground mb-1 px-1">Pod logs for {job.type} hook</p>
                          <LogViewer
                            namespace={namespace}
                            environmentName={environmentName}
                            hookType={job.type}
                            className="h-[400px] text-xs"
                          />
                        </div>
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              )
            })}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}
