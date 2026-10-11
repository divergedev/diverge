import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useListAgentTasks } from '@/api/queries'
import { StatusBadge, formatAgentTaskPhase } from '@/components/StatusBadge'
import { CreateAgentTaskModal } from '@/components/CreateAgentTaskModal'
import { EmptyState } from '@/components/EmptyState'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { isSafeExternalUrl } from '@/lib/utils'
import { Bot, Plus, Search, ExternalLink, AlertCircle } from 'lucide-react'

const PAGE_SIZE = 25

/**
 * Dashboard page listing all autonomous AgentTasks with search, pagination, and task creation.
 */
export default function AgentTasks() {
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(0)
  const [createOpen, setCreateOpen] = useState(false)
  const { data, isLoading, error, refetch } = useListAgentTasks()

  const tasks = data?.tasks ?? []
  const filtered = search
    ? tasks.filter((t) =>
        t.name.toLowerCase().includes(search.toLowerCase()) ||
        t.namespace.toLowerCase().includes(search.toLowerCase()) ||
        (t.spec?.objective ?? '').toLowerCase().includes(search.toLowerCase()) ||
        (t.spec?.repository?.url ?? '').toLowerCase().includes(search.toLowerCase()),
      )
    : tasks

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const currentPage = Math.min(page, totalPages - 1)
  const pagedTasks = filtered.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE)

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">Agent Tasks</h1>
          <p className="text-muted-foreground">Autonomous sandboxed coding tasks</p>
        </div>
        <Button onClick={() => setCreateOpen(true)}><Plus className="h-4 w-4 mr-2" />Create Task</Button>
      </div>

      <div className="flex items-center gap-4">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder="Search tasks..."
            value={search}
            onChange={(e) => {
              setSearch(e.target.value)
              setPage(0)
            }}
            className="pl-9"
          />
        </div>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-16">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary" />
        </div>
      ) : error ? (
        <div className="text-center py-16 space-y-4" role="alert">
          <AlertCircle className="h-10 w-10 text-destructive mx-auto" />
          <h2 className="text-xl font-semibold">Failed to load agent tasks</h2>
          <p className="text-muted-foreground">{error.message}</p>
          <Button variant="outline" onClick={() => refetch()}>Retry</Button>
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={Bot}
          title="No agent tasks yet"
          description="Launch an autonomous coding task in an isolated sandbox."
          action={<Button onClick={() => setCreateOpen(true)}><Plus className="h-4 w-4 mr-2" />Create Task</Button>}
        />
      ) : (
        <>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Namespace</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Objective</TableHead>
                <TableHead>Repository</TableHead>
                <TableHead>Iterations</TableHead>
                <TableHead>Cost (USD)</TableHead>
                <TableHead>PR</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {pagedTasks.map((task) => {
                const phaseLabel = formatAgentTaskPhase(task.status?.phase)
                const safePrUrl = isSafeExternalUrl(task.status?.prUrl) ? task.status?.prUrl : undefined
                return (
                  <TableRow key={`${task.namespace}/${task.name}`}>
                    <TableCell>
                      <Link to={`/tasks/${task.namespace}/${task.name}`} className="font-medium text-primary hover:underline">
                        {task.name}
                      </Link>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{task.namespace}</TableCell>
                    <TableCell><StatusBadge phase={phaseLabel} /></TableCell>
                    <TableCell className="max-w-xs truncate" title={task.spec?.objective}>
                      {task.spec?.objective || '—'}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground max-w-[200px] truncate" title={task.spec?.repository?.url}>
                      {task.spec?.repository?.url || '—'}
                    </TableCell>
                    <TableCell>
                      {task.status?.iteration ?? 0} / {task.spec?.maxIterations || '∞'}
                    </TableCell>
                    <TableCell>
                      ${task.status?.costUsd || '0.00'}{task.spec?.budgetUsd ? ` / $${task.spec.budgetUsd}` : ''}
                    </TableCell>
                    <TableCell>
                      {safePrUrl ? (
                        <a
                          href={safePrUrl}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex items-center gap-1 text-primary hover:underline text-xs"
                        >
                          PR <ExternalLink className="h-3 w-3" />
                        </a>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>

          {totalPages > 1 && (
            <div className="flex items-center justify-between text-sm text-muted-foreground">
              <span>
                Page {currentPage + 1} of {totalPages} ({filtered.length} tasks)
              </span>
              <div className="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={currentPage === 0}
                  onClick={() => setPage((p) => Math.max(0, p - 1))}
                >
                  Previous
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={currentPage >= totalPages - 1}
                  onClick={() => setPage((p) => Math.min(totalPages - 1, p + 1))}
                >
                  Next
                </Button>
              </div>
            </div>
          )}
        </>
      )}

      <CreateAgentTaskModal open={createOpen} onClose={() => setCreateOpen(false)} />
    </div>
  )
}
