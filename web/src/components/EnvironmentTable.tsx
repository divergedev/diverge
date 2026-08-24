import { Link } from 'react-router-dom'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from '@/components/ui/table'
import { StatusBadge } from '@/components/StatusBadge'
import { TTLCountdown } from '@/components/TTLCountdown'
import { ExternalLink, Copy } from 'lucide-react'
import { Button } from '@/components/ui/button'
import type { Environment } from '@/api/gen/diverge/v1alpha1/environment_pb'
import { formatDistanceToNow } from 'date-fns'

export function EnvironmentTable({ environments, isLoading }: { environments: Environment[]; isLoading: boolean }) {
  if (isLoading) {
    return <div className="flex justify-center py-8"><div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary" /></div>
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Namespace</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>Branch</TableHead>
          <TableHead>TTL</TableHead>
          <TableHead>Age</TableHead>
          <TableHead>Preview</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {environments.map((env) => (
          <TableRow key={`${env.namespace}/${env.name}`}>
            <TableCell>
              <Link to={`/environments/${env.namespace}/${env.name}`} className="font-medium text-primary hover:underline">
                {env.name}
              </Link>
            </TableCell>
            <TableCell className="text-muted-foreground">{env.namespace}</TableCell>
            <TableCell><StatusBadge phase={env.status?.phase ?? ''} /></TableCell>
            <TableCell className="text-sm">{env.spec?.source?.branch ?? '—'}</TableCell>
            <TableCell><TTLCountdown expiresAt={env.status?.expiresAt?.toDate?.()?.toISOString?.()} /></TableCell>
            <TableCell className="text-muted-foreground text-sm">
              {env.metadata?.creationTimestamp ? formatDistanceToNow(env.metadata.creationTimestamp.toDate(), { addSuffix: true }) : '—'}
            </TableCell>
            <TableCell>
              {env.status?.url ? (
                <div className="flex items-center gap-1">
                  <a href={env.status.url} target="_blank" rel="noreferrer" className="text-primary hover:underline">
                    <ExternalLink className="h-4 w-4" />
                  </a>
                  <Button size="icon" variant="ghost" className="h-6 w-6" onClick={() => navigator.clipboard.writeText(env.status?.url ?? '')}>
                    <Copy className="h-3 w-3" />
                  </Button>
                </div>
              ) : '—'}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
