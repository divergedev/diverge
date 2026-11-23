import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useListPreviewGroups } from '@/api/queries'
import { useWatchPreviewGroups } from '@/hooks/useWatch'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { StatusBadge } from '@/components/StatusBadge'
import { EmptyState } from '@/components/EmptyState'
import { ComposePreviewGroupModal } from '@/components/ComposePreviewGroupModal'
import { countDistinctGroupMRs, parseGroupMembers, previewGroupPath } from '@/lib/previewgroup'
import { GitBranch, Plus } from 'lucide-react'

export default function PreviewGroups() {
  const { data, isLoading } = useListPreviewGroups()
  useWatchPreviewGroups()
  const [composeOpen, setComposeOpen] = useState(false)
  const groups = data?.previewGroups ?? []

  if (isLoading) {
    return <div className="flex justify-center py-16"><div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary" /></div>
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold">Preview Groups</h1>
          <p className="text-muted-foreground">Combine multiple services and open MRs behind a shared routing header</p>
        </div>
        <Button type="button" onClick={() => setComposeOpen(true)}>
          <Plus className="h-4 w-4 mr-2" aria-hidden="true" />
          Compose Preview Group
        </Button>
      </div>

      {groups.length === 0 ? (
        <EmptyState
          icon={GitBranch}
          title="No preview groups"
          description="Compose open frontend and backend MRs into a single end-to-end preview environment."
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Scope</TableHead>
              <TableHead>Routing Header</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Services</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {groups.map((pg) => {
              const members = parseGroupMembers(pg.annotations)
              const mrCount = countDistinctGroupMRs(members)
              const routing = pg.spec?.routing
              const header =
                routing?.headerKey && routing?.headerValue
                  ? `${routing.headerKey}: ${routing.headerValue}`
                  : '—'
              return (
                <TableRow key={`${pg.namespace || '_cluster'}/${pg.name}`}>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <Link to={previewGroupPath(pg)} className="font-medium text-primary hover:underline">
                        {pg.name}
                      </Link>
                      {mrCount > 1 ? (
                        <Badge variant="secondary" className="text-[10px]">
                          {mrCount} MRs
                        </Badge>
                      ) : members.length > 1 ? (
                        <Badge variant="secondary" className="text-[10px]">
                          {members.length} joined
                        </Badge>
                      ) : null}
                    </div>
                  </TableCell>
                  <TableCell className="text-muted-foreground font-mono text-xs">
                    {pg.namespace || 'cluster'}
                  </TableCell>
                  <TableCell className="font-mono text-xs">{header}</TableCell>
                  <TableCell><StatusBadge phase={pg.status?.phase ?? ''} /></TableCell>
                  <TableCell>{pg.spec?.services?.length ?? 0}</TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}

      {composeOpen && (
        <ComposePreviewGroupModal open={composeOpen} onClose={() => setComposeOpen(false)} />
      )}
    </div>
  )
}
