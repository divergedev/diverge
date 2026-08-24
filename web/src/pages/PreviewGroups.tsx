import { Link } from 'react-router-dom'
import { useListPreviewGroups } from '@/api/queries'
import { useWatchPreviewGroups } from '@/hooks/useWatch'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from '@/components/ui/table'
import { StatusBadge } from '@/components/StatusBadge'
import { EmptyState } from '@/components/EmptyState'
import { GitBranch } from 'lucide-react'

export default function PreviewGroups() {
  const { data, isLoading } = useListPreviewGroups()
  useWatchPreviewGroups()
  const groups = data?.previewGroups ?? []

  if (isLoading) {
    return <div className="flex justify-center py-16"><div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary" /></div>
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold">Preview Groups</h1>
        <p className="text-muted-foreground">Multi-service preview environment templates</p>
      </div>

      {groups.length === 0 ? (
        <EmptyState icon={GitBranch} title="No preview groups" description="Preview groups let you define multi-service preview environments." />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Namespace</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Services</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {groups.map((pg) => (
              <TableRow key={`${pg.namespace}/${pg.name}`}>
                <TableCell>
                  <Link to={`/preview-groups/${pg.namespace}/${pg.name}`} className="font-medium text-primary hover:underline">{pg.name}</Link>
                </TableCell>
                <TableCell className="text-muted-foreground">{pg.namespace}</TableCell>
                <TableCell><StatusBadge phase={pg.status?.phase ?? ''} /></TableCell>
                <TableCell>{pg.spec?.services?.length ?? 0}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
