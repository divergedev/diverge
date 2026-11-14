import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { flushSync } from 'react-dom'
import { Link } from 'react-router-dom'
import type { EnvironmentCostItem } from '@/api/gen/diverge/v1alpha1/cluster_pb'
import { useGetClusterInfo, useGetCostSummary } from '@/api/queries'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { StatusBadge } from '@/components/StatusBadge'
import { EmptyState } from '@/components/EmptyState'
import {
  DollarSign,
  Cpu,
  Clock,
  Sparkles,
  AlertTriangle,
  Bot,
  Layers,
  ChevronDown,
} from 'lucide-react'
import { formatCurrencyUSD, formatHourlyCostUSD } from '@/lib/finops'
import { cn } from '@/lib/utils'

const textCollator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

type ElementWithViewTransition = HTMLElement & {
  startViewTransition?: (callback: () => void) => void
}

type DocumentWithViewTransition = Document & {
  startViewTransition?: (callback: () => void) => void
}

type ElementWithMoveBefore = HTMLElement & {
  moveBefore?: (movedNode: Node, referenceNode: Node | null) => void
}

function reorderTableRows(tbody: HTMLTableSectionElement, orderedRows: HTMLTableRowElement[]) {
  const container = tbody as unknown as ElementWithMoveBefore
  const move = Object.hasOwn(Element.prototype, 'moveBefore') && typeof container.moveBefore === 'function'
    ? (row: HTMLTableRowElement) => container.moveBefore!(row, null)
    : (row: HTMLTableRowElement) => tbody.append(row)

  for (const row of orderedRows) {
    move(row)
  }
}

function reorderWithTransition(
  table: HTMLTableElement,
  tbody: HTMLTableSectionElement,
  orderedRows: HTMLTableRowElement[],
  onCommitState: () => void,
) {
  const update = () => {
    reorderTableRows(tbody, orderedRows)
    flushSync(onCommitState)
  }
  if (orderedRows.length > 200 || typeof document === 'undefined') {
    update()
    return
  }
  const scopedTable = table as unknown as ElementWithViewTransition
  const doc = document as DocumentWithViewTransition
  if (typeof scopedTable.startViewTransition === 'function') {
    scopedTable.startViewTransition(update)
  } else if (typeof doc.startViewTransition === 'function') {
    doc.startViewTransition(update)
  } else {
    update()
  }
}

type SortKey = 'name' | 'hourly' | 'accrued' | 'uptime'
type SortDirection = 'ascending' | 'descending'

function compareConsumerItems(
  a: EnvironmentCostItem,
  b: EnvironmentCostItem,
  key: SortKey,
  direction: SortDirection,
): number {
  const factor = direction === 'ascending' ? 1 : -1
  switch (key) {
    case 'name':
      return factor * textCollator.compare(a.name, b.name)
    case 'hourly':
      return factor * (a.hourlyCostUsd - b.hourlyCostUsd)
    case 'accrued':
      return factor * (a.accruedCostUsd - b.accruedCostUsd)
    case 'uptime':
      return factor * (a.uptimeHours - b.uptimeHours)
  }
}

export default function CostAnalytics() {
  const [selectedNamespace, setSelectedNamespace] = useState('')
  const [sortState, setSortState] = useState<{ key: SortKey; direction: SortDirection }>({
    key: 'hourly',
    direction: 'descending',
  })
  const nsFilterId = useId()
  const nsTableCaptionId = useId()
  const svcTableCaptionId = useId()
  const consumersTableId = useId()
  const consumersCaptionId = useId()

  const tableRef = useRef<HTMLTableElement | null>(null)
  const tbodyRef = useRef<HTMLTableSectionElement | null>(null)
  const sortStatusRef = useRef<HTMLParagraphElement | null>(null)

  const { data: clusterInfo } = useGetClusterInfo()
  const { data: summary, isLoading, error } = useGetCostSummary(selectedNamespace || undefined)

  const performColumnSort = (sourceButton: HTMLButtonElement) => {
    const table = tableRef.current
    const tbody = tbodyRef.current
    if (!table || !tbody) return

    const th = sourceButton.closest('th')
    if (!th || !table.tHead?.rows[0]) return

    const nextKey = ((th.dataset.sortKey as SortKey | undefined) ?? 'hourly')
    const direction: SortDirection =
      sortState.key === nextKey && sortState.direction === 'ascending' ? 'descending' : 'ascending'

    const colIndex = th.cellIndex
    const colType = th.dataset.type ?? 'text'
    const factor = direction === 'ascending' ? 1 : -1

    const rowsWithValues = Array.from(tbody.rows).map((row) => {
      const cell = row.cells[colIndex]
      const raw = cell?.dataset.value ?? cell?.textContent?.trim() ?? ''
      return { row, raw, num: Number(raw) }
    })

    rowsWithValues.sort((a, b) => {
      if (colType === 'number') {
        const diff = (Number.isFinite(a.num) ? a.num : 0) - (Number.isFinite(b.num) ? b.num : 0)
        return factor * diff
      }
      return factor * textCollator.compare(a.raw, b.raw)
    })

    const orderedRows = rowsWithValues.map((entry) => entry.row)
    reorderWithTransition(table, tbody, orderedRows, () => {
      setSortState({ key: nextKey, direction })
    })

    if (sortStatusRef.current) {
      const colLabel = sourceButton.textContent?.trim() ?? 'column'
      sortStatusRef.current.textContent = `Sorted by ${colLabel}, ${direction}, ${orderedRows.length} rows`
    }
  }

  const performSortRef = useRef(performColumnSort)
  performSortRef.current = performColumnSort

  useEffect(() => {
    const table = tableRef.current
    if (!table) return
    const handleCommand = (event: Event) => {
      const cmdEvent = event as Event & { command?: string; source?: Element | null }
      if (cmdEvent.command !== '--sort') return
      if (cmdEvent.source instanceof HTMLButtonElement) {
        performSortRef.current(cmdEvent.source)
      }
    }
    table.addEventListener('command', handleCommand)
    return () => table.removeEventListener('command', handleCommand)
  }, [Boolean(summary), isLoading, Boolean(error)])

  const handleSortButtonClick = (e: React.MouseEvent<HTMLButtonElement>) => {
    // Fallback for browsers where Invoker Commands (CommandEvent) are not supported
    if (typeof window !== 'undefined' && 'CommandEvent' in window) {
      return
    }
    performColumnSort(e.currentTarget)
  }

  const namespaces = clusterInfo?.namespaces ?? []
  const byNamespace = summary?.byNamespace ?? []
  const byService = summary?.byService ?? []
  const topConsumers = summary?.topConsumers ?? []
  const sortedConsumers = useMemo(
    () => [...topConsumers].sort((a, b) => compareConsumerItems(a, b, sortState.key, sortState.direction)),
    [topConsumers, sortState],
  )
  const rateCard = summary?.rateCard

  const totalSavingsHourly = (summary?.hibernationSavingsHourlyUsd ?? 0) + (summary?.deltaRoutingSavingsHourlyUsd ?? 0)
  const budgetWarnings = byNamespace.filter((ns) => ns.budgetWarning)
  const ttlWarnings = topConsumers.filter((item) => item.ttlWarning)

  return (
    <div className="space-y-6">
      <div className="flex items-end justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-2xl font-bold">Cost &amp; Usage Analytics</h1>
          <p className="text-muted-foreground">
            Per-namespace, per-service, and per-PR compute attribution, hibernation savings, and quota health
          </p>
        </div>
        <div className="flex flex-col gap-1 min-w-[200px]">
          <label htmlFor={nsFilterId} className="text-xs font-medium text-muted-foreground">
            Namespace scope
          </label>
          <Select
            id={nsFilterId}
            value={selectedNamespace}
            onChange={(e) => setSelectedNamespace(e.target.value)}
          >
            <option value="">All namespaces</option>
            {namespaces.map((ns) => (
              <option key={ns} value={ns}>{ns}</option>
            ))}
          </Select>
        </div>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-16">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary" />
        </div>
      ) : error ? (
        <div className="text-center py-16 space-y-2" role="alert">
          <h2 className="text-xl font-semibold">Failed to load cost analytics</h2>
          <p className="text-muted-foreground text-sm">{error.message}</p>
        </div>
      ) : (
        <>

      {(budgetWarnings.length > 0 || ttlWarnings.length > 0) && (
        <div className="bg-amber-500/10 border border-amber-500/20 text-amber-700 dark:text-amber-400 px-4 py-3 rounded-md space-y-1 text-sm" role="status">
          <div className="flex items-center gap-2 font-semibold">
            <AlertTriangle className="h-4 w-4 flex-shrink-0" aria-hidden="true" />
            <span>FinOps Budget &amp; Lifecycle Warnings ({budgetWarnings.length + ttlWarnings.length})</span>
          </div>
          {budgetWarnings.map((ns) => (
            <p key={`ns-warn-${ns.namespace}`} className="text-xs pl-6">
              Namespace <code className="font-mono">{ns.namespace}</code>: {ns.warningReason}
            </p>
          ))}
          {ttlWarnings.slice(0, 5).map((item) => (
            <p key={`ttl-warn-${item.namespace}/${item.name}`} className="text-xs pl-6">
              Environment{' '}
              <Link to={`/environments/${item.namespace}/${item.name}`} className="underline font-mono">
                {item.namespace}/{item.name}
              </Link>
              : {item.ttlWarningReason}
            </p>
          ))}
        </div>
      )}

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Fleet Hourly Burn</CardTitle>
            <DollarSign className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold font-mono">
              {formatHourlyCostUSD(summary?.totalHourlyCostUsd ?? 0)}
            </div>
            <p className="text-xs text-muted-foreground mt-1">
              ~${(summary?.monthlyRunRateUsd ?? 0).toFixed(2)}/mo run-rate · {formatCurrencyUSD(summary?.totalAccruedCostUsd ?? 0)} accrued
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Estimated Hourly Savings</CardTitle>
            <Sparkles className="h-4 w-4 text-emerald-500" aria-hidden="true" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold font-mono text-emerald-600 dark:text-emerald-400">
              {formatHourlyCostUSD(totalSavingsHourly)}
            </div>
            <p className="text-xs text-muted-foreground mt-1">
              {formatHourlyCostUSD(summary?.hibernationSavingsHourlyUsd ?? 0)} hibernation ·{' '}
              {formatHourlyCostUSD(summary?.deltaRoutingSavingsHourlyUsd ?? 0)} delta routing
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Active Compute Footprint</CardTitle>
            <Cpu className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold font-mono">
              {(summary?.totalCpuCores ?? 0).toFixed(2)} vCPU · {(summary?.totalMemoryGib ?? 0).toFixed(2)} GiB
            </div>
            <p className="text-xs text-muted-foreground mt-1">
              {summary?.activeEnvironments ?? 0} active · {summary?.hibernatedEnvironments ?? 0} hibernated
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Active Compute-Hours &amp; AI</CardTitle>
            <Clock className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold font-mono">
              {(summary?.totalComputeHours ?? 0).toFixed(1)} svc-hrs
            </div>
            <p className="text-xs text-muted-foreground mt-1 flex items-center gap-1">
              <Bot className="h-3.5 w-3.5" aria-hidden="true" />
              AgentTask LLM spend: {formatCurrencyUSD(summary?.agentTaskCostUsd ?? 0)}
            </p>
          </CardContent>
        </Card>
      </div>

      {topConsumers.length === 0 && byNamespace.length === 0 ? (
        <EmptyState
          icon={Layers}
          title="No preview usage recorded yet"
          description="Active and hibernated preview environments will appear here with real-time cost attribution."
        />
      ) : (
        <>
          <div className="grid gap-6 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <h2 className="text-base font-semibold leading-none tracking-tight">Per-Namespace Breakdown &amp; Budgets</h2>
              </CardHeader>
              <CardContent>
                <div tabIndex={0} role="region" aria-labelledby={nsTableCaptionId} className="finops-table-wrapper">
                  <table className="text-sm">
                    <caption id={nsTableCaptionId} className="visually-hidden">
                      Per-namespace preview cost, hibernation savings, and quota utilization
                    </caption>
                    <thead>
                      <tr className="border-b text-left text-muted-foreground">
                        <th scope="col" className="py-2 pr-3 font-medium">Namespace</th>
                        <th scope="col" className="py-2 px-3 font-medium">Envs</th>
                        <th scope="col" className="py-2 px-3 font-medium">Hourly Burn</th>
                        <th scope="col" className="py-2 px-3 font-medium">Saved/hr</th>
                        <th scope="col" className="py-2 pl-3 font-medium min-w-[140px]">Quota Utilization</th>
                      </tr>
                    </thead>
                    <tbody>
                      {byNamespace.map((ns) => {
                        const pct = Math.max(ns.budgetUtilizationPercent, 0)
                        const hasQuota = Boolean(
                          ns.quota && (
                            ns.quota.maxHourlyCostUsd > 0 ||
                            ns.quota.maxActiveEnvironments > 0 ||
                            ns.quota.maxCnpgClusters > 0
                          ),
                        )
                        const progLabelId = `ns-prog-${ns.namespace}`
                        return (
                          <tr key={ns.namespace} className="border-b last:border-0">
                            <th id={progLabelId} scope="row" className="py-2.5 pr-3 font-mono font-medium text-left">
                              {ns.namespace}
                            </th>
                            <td className="py-2.5 px-3 text-muted-foreground">
                              {ns.activeEnvironments} active · {ns.hibernatedEnvironments} idle
                            </td>
                            <td className="py-2.5 px-3 font-mono">{formatHourlyCostUSD(ns.hourlyCostUsd)}</td>
                            <td className="py-2.5 px-3 font-mono text-emerald-600 dark:text-emerald-400">
                              {formatHourlyCostUSD(ns.hibernationSavingsHourlyUsd)}
                            </td>
                            <td className="py-2.5 pl-3">
                              {hasQuota ? (
                                <div className="space-y-1">
                                  <div className="flex items-center justify-between text-xs font-mono">
                                    <span>{Math.round(pct)}%</span>
                                    <span className="text-muted-foreground">{ns.quota?.quotaExceedPolicy}</span>
                                  </div>
                                  <progress
                                    value={Math.min(pct, 100)}
                                    max={100}
                                    aria-labelledby={progLabelId}
                                    className={cn(
                                      'budget-progress',
                                      pct >= 100
                                        ? 'budget-progress--critical'
                                        : pct >= 80
                                          ? 'budget-progress--warning'
                                          : undefined,
                                    )}
                                  />
                                </div>
                              ) : (
                                <span className="text-xs text-muted-foreground">No cap configured</span>
                              )}
                            </td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <h2 className="text-base font-semibold leading-none tracking-tight">Per-Service Attribution</h2>
              </CardHeader>
              <CardContent>
                <div tabIndex={0} role="region" aria-labelledby={svcTableCaptionId} className="finops-table-wrapper">
                  <table className="text-sm">
                    <caption id={svcTableCaptionId} className="visually-hidden">
                      Per-service preview resource footprint and hourly cost attribution
                    </caption>
                    <thead>
                      <tr className="border-b text-left text-muted-foreground">
                        <th scope="col" className="py-2 pr-3 font-medium">Service</th>
                        <th scope="col" className="py-2 px-3 font-medium">Previews</th>
                        <th scope="col" className="py-2 px-3 font-medium">CPU / Memory</th>
                        <th scope="col" className="py-2 pl-3 font-medium">Attributed Burn</th>
                      </tr>
                    </thead>
                    <tbody>
                      {byService.map((svc) => (
                        <tr key={svc.serviceName} className="border-b last:border-0">
                          <th scope="row" className="py-2.5 pr-3 font-mono font-medium text-left">
                            {svc.serviceName}
                          </th>
                          <td className="py-2.5 px-3 text-muted-foreground">
                            {svc.activeCount} active / {svc.environmentCount} total
                          </td>
                          <td className="py-2.5 px-3 font-mono text-xs">
                            {svc.cpuCores.toFixed(2)} vCPU · {svc.memoryGib.toFixed(2)} GiB
                          </td>
                          <td className="py-2.5 pl-3 font-mono">{formatHourlyCostUSD(svc.hourlyCostUsd)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardHeader className="flex flex-row items-center justify-between gap-4 flex-wrap">
              <div>
                <h2 className="text-base font-semibold leading-none tracking-tight">Top Consumers (Per-PR Attribution)</h2>
                <p className="text-xs text-muted-foreground mt-1">
                  Click column headers to sort environments by burn rate, accrued spend, or uptime
                </p>
              </div>
              {rateCard && (
                <div className="text-xs font-mono text-muted-foreground bg-muted px-2.5 py-1 rounded">
                  Rate card: ${rateCard.vcpuHourUsd.toFixed(3)}/vCPU-hr · ${rateCard.memoryGibHourUsd.toFixed(3)}/GiB-hr ·
                  {' '}${rateCard.databaseSchemaHourUsd.toFixed(3)}/schema-hr · ${rateCard.databaseDedicatedHourUsd.toFixed(3)}/DB-hr
                </div>
              )}
            </CardHeader>
            <CardContent>
              <p ref={sortStatusRef} role="status" className="visually-hidden" />
              <div tabIndex={0} role="region" aria-labelledby={consumersCaptionId} className="finops-table-wrapper">
                <table id={consumersTableId} ref={tableRef} className="text-sm">
                  <caption id={consumersCaptionId} className="visually-hidden">
                    Preview environments ranked by infrastructure cost and resource usage
                  </caption>
                  <thead>
                    <tr className="border-b text-left text-muted-foreground">
                      <th
                        scope="col"
                        data-type="text"
                        data-sort-key="name"
                        aria-sort={sortState.key === 'name' ? sortState.direction : undefined}
                        className="font-medium"
                      >
                        <button
                          type="button"
                          onClick={handleSortButtonClick}
                          {...({ commandfor: consumersTableId, command: '--sort' } as Record<string, string>)}
                        >
                          Environment
                          <ChevronDown className="h-3.5 w-3.5 sort-icon" aria-hidden="true" />
                        </button>
                      </th>
                      <th scope="col" className="py-2 px-3 font-medium">Namespace</th>
                      <th scope="col" className="py-2 px-3 font-medium">PR / Branch</th>
                      <th scope="col" className="py-2 px-3 font-medium">State</th>
                      <th scope="col" className="py-2 px-3 font-medium">Resources</th>
                      <th
                        scope="col"
                        data-type="number"
                        data-sort-key="hourly"
                        aria-sort={sortState.key === 'hourly' ? sortState.direction : undefined}
                        className="font-medium"
                      >
                        <button
                          type="button"
                          onClick={handleSortButtonClick}
                          {...({ commandfor: consumersTableId, command: '--sort' } as Record<string, string>)}
                        >
                          Hourly Burn
                          <ChevronDown className="h-3.5 w-3.5 sort-icon" aria-hidden="true" />
                        </button>
                      </th>
                      <th
                        scope="col"
                        data-type="number"
                        data-sort-key="accrued"
                        aria-sort={sortState.key === 'accrued' ? sortState.direction : undefined}
                        className="font-medium"
                      >
                        <button
                          type="button"
                          onClick={handleSortButtonClick}
                          {...({ commandfor: consumersTableId, command: '--sort' } as Record<string, string>)}
                        >
                          Accrued Spend
                          <ChevronDown className="h-3.5 w-3.5 sort-icon" aria-hidden="true" />
                        </button>
                      </th>
                      <th
                        scope="col"
                        data-type="number"
                        data-sort-key="uptime"
                        aria-sort={sortState.key === 'uptime' ? sortState.direction : undefined}
                        className="font-medium"
                      >
                        <button
                          type="button"
                          onClick={handleSortButtonClick}
                          {...({ commandfor: consumersTableId, command: '--sort' } as Record<string, string>)}
                        >
                          Uptime
                          <ChevronDown className="h-3.5 w-3.5 sort-icon" aria-hidden="true" />
                        </button>
                      </th>
                    </tr>
                  </thead>
                  <tbody ref={tbodyRef}>
                    {sortedConsumers.map((item) => (
                      <tr key={`${item.namespace}/${item.name}`} className="border-b last:border-0">
                        <th scope="row" data-value={item.name} className="py-2.5 px-3 font-medium text-left">
                          <Link
                            to={`/environments/${item.namespace}/${item.name}`}
                            className="text-primary hover:underline font-mono"
                          >
                            {item.name}
                          </Link>
                          {item.ttlWarning && (
                            <span
                              className="ml-2 inline-flex items-center rounded-full bg-amber-500/10 px-2 py-0.5 text-[11px] font-sans font-medium text-amber-600 dark:text-amber-400"
                              title={item.ttlWarningReason}
                            >
                              TTL Warning
                            </span>
                          )}
                        </th>
                        <td className="py-2.5 px-3 text-muted-foreground font-mono text-xs">{item.namespace}</td>
                        <td className="py-2.5 px-3 text-xs">
                          {item.mr ? <span className="font-mono font-medium mr-1.5">#{item.mr}</span> : null}
                          <span className="text-muted-foreground">{item.branch || '—'}</span>
                        </td>
                        <td className="py-2.5 px-3">
                          <StatusBadge phase={item.phase} />
                          {item.hibernated && (
                            <span className="ml-1.5 inline-flex items-center rounded-full bg-emerald-500/10 px-2 py-0.5 text-xs font-medium text-emerald-600 dark:text-emerald-400">
                              Idle (-{formatHourlyCostUSD(item.hibernationSavingsHourlyUsd)})
                            </span>
                          )}
                        </td>
                        <td className="py-2.5 px-3 font-mono text-xs text-muted-foreground">
                          {item.serviceCount} svc · {item.databaseMode}
                        </td>
                        <td data-value={String(item.hourlyCostUsd)} className="py-2.5 px-3 font-mono">
                          {formatHourlyCostUSD(item.hourlyCostUsd)}
                        </td>
                        <td data-value={String(item.accruedCostUsd)} className="py-2.5 px-3 font-mono">
                          {formatCurrencyUSD(item.accruedCostUsd)}
                        </td>
                        <td data-value={String(item.uptimeHours)} className="py-2.5 px-3 font-mono text-muted-foreground">
                          {item.uptimeHours.toFixed(1)}h
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </CardContent>
          </Card>
            </>
          )}
        </>
      )}
    </div>
  )
}
