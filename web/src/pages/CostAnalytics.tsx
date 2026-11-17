import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { flushSync } from 'react-dom'
import { Link } from 'react-router-dom'
import type { EnvironmentCostItem } from '@/api/gen/diverge/v1alpha1/cluster_pb'
import { useGetClusterInfo, useGetCostSummary } from '@/api/queries'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
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
  Download,
  Activity,
} from 'lucide-react'
import {
  buildCostSummaryCsv,
  buildCostSummaryJson,
  downloadTextFile,
  formatCurrencyUSD,
  formatHourlyCostUSD,
} from '@/lib/finops'
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
  const [windowDays, setWindowDays] = useState<7 | 30>(30)
  const [includeHistorical, setIncludeHistorical] = useState(false)
  const [sortState, setSortState] = useState<{ key: SortKey; direction: SortDirection }>({
    key: 'hourly',
    direction: 'descending',
  })
  const nsFilterId = useId()
  const nsTableCaptionId = useId()
  const svcTableCaptionId = useId()
  const consumersTableId = useId()
  const consumersCaptionId = useId()
  const trendTitleId = useId()
  const trendDescId = useId()
  const trendTableCaptionId = useId()

  const tableRef = useRef<HTMLTableElement | null>(null)
  const tbodyRef = useRef<HTMLTableSectionElement | null>(null)
  const sortStatusRef = useRef<HTMLParagraphElement | null>(null)

  const { data: clusterInfo } = useGetClusterInfo()
  const { data: summary, isLoading, error } = useGetCostSummary(selectedNamespace || undefined, {
    windowDays,
    includeHistorical,
  })

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
  const byCluster = summary?.byCluster ?? []
  const dailyTrend = summary?.dailyTrend ?? []
  const topConsumers = summary?.topConsumers ?? []
  const historicalConsumers = summary?.historicalConsumers ?? []
  const combinedConsumers = useMemo(
    () => (includeHistorical ? [...topConsumers, ...historicalConsumers] : topConsumers),
    [topConsumers, historicalConsumers, includeHistorical],
  )
  const sortedConsumers = useMemo(
    () => [...combinedConsumers].sort((a, b) => compareConsumerItems(a, b, sortState.key, sortState.direction)),
    [combinedConsumers, sortState],
  )
  const rateCard = summary?.rateCard
  const hasRemoteClusters = byCluster.some((cl) => cl.cluster && cl.cluster !== 'local')
  const trendTotals = useMemo(() => {
    let accrued = 0
    let saved = 0
    let maxDaily = 0
    for (const pt of dailyTrend) {
      const daySaved = pt.hibernationSavingsUsd + pt.deltaRoutingSavingsUsd
      accrued += pt.accruedCostUsd
      saved += daySaved
      maxDaily = Math.max(maxDaily, pt.accruedCostUsd, daySaved)
    }
    return { accrued, saved, maxDaily: maxDaily > 0 ? maxDaily : 0.01 }
  }, [dailyTrend])

  const totalSavingsHourly = (summary?.hibernationSavingsHourlyUsd ?? 0) + (summary?.deltaRoutingSavingsHourlyUsd ?? 0)
  const budgetWarnings = byNamespace.filter((ns) => ns.budgetWarning)
  const ttlWarnings = topConsumers.filter((item) => item.ttlWarning)
  const exportDisabled = isLoading || Boolean(error) || !summary
  const scopeSlug = selectedNamespace ? selectedNamespace : 'all-namespaces'

  const announcePolite = (msg: string) => {
    if (sortStatusRef.current) {
      sortStatusRef.current.textContent = msg
    }
  }

  const handleExportCsv = () => {
    if (exportDisabled || !summary) return
    const csvContent = buildCostSummaryCsv(sortedConsumers)
    downloadTextFile(`diverge-finops-${scopeSlug}.csv`, csvContent, 'text/csv;charset=utf-8')
    announcePolite(`Exported FinOps report as CSV (${sortedConsumers.length} environments)`)
  }

  const handleExportJson = () => {
    if (exportDisabled || !summary) return
    const jsonContent = buildCostSummaryJson(summary)
    downloadTextFile(`diverge-finops-${scopeSlug}.json`, jsonContent, 'application/json;charset=utf-8')
    announcePolite('Exported FinOps report as JSON')
  }

  return (
    <div className="space-y-6">
      <p ref={sortStatusRef} role="status" className="visually-hidden" />
      <div className="flex items-end justify-between gap-4 flex-wrap">
        <div>
          <div className="flex items-center gap-2.5 flex-wrap">
            <h1 className="text-2xl font-bold">Cost &amp; Usage Analytics</h1>
            {summary && (
              summary.opencostEnabled ? (
                <span className="inline-flex items-center gap-1.5 rounded-full bg-emerald-500/10 border border-emerald-500/20 px-2.5 py-0.5 text-xs font-medium text-emerald-700 dark:text-emerald-400">
                  <Activity className="h-3.5 w-3.5" aria-hidden="true" />
                  OpenCost Live
                </span>
              ) : (
                <span className="inline-flex items-center gap-1.5 rounded-full bg-muted px-2.5 py-0.5 text-xs font-medium text-muted-foreground">
                  Rate-Card Estimate
                </span>
              )
            )}
          </div>
          <p className="text-muted-foreground">
            Per-namespace, per-service, and per-PR compute attribution, hibernation savings, and quota health
          </p>
        </div>
        <div className="flex items-end gap-2 flex-wrap">
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
          <div className="flex items-center gap-1.5" role="group" aria-label="Historical trend window">
            <Button
              type="button"
              size="sm"
              variant={windowDays === 7 ? 'secondary' : 'outline'}
              aria-pressed={windowDays === 7}
              onClick={() => setWindowDays(7)}
              className="min-h-9 px-3"
            >
              7d
            </Button>
            <Button
              type="button"
              size="sm"
              variant={windowDays === 30 ? 'secondary' : 'outline'}
              aria-pressed={windowDays === 30}
              onClick={() => setWindowDays(30)}
              className="min-h-9 px-3"
            >
              30d
            </Button>
          </div>
          <Button
            type="button"
            size="sm"
            variant={includeHistorical ? 'secondary' : 'outline'}
            aria-pressed={includeHistorical}
            onClick={() => setIncludeHistorical((prev) => !prev)}
            className="min-h-9"
          >
            Include closed PRs
            {(summary?.historicalEnvironments ?? 0) > 0 ? ` (${summary?.historicalEnvironments})` : ''}
          </Button>
          <div className="flex items-center gap-2" role="group" aria-label="Export FinOps report">
            <Button
              type="button"
              variant="outline"
              size="sm"
              aria-disabled={exportDisabled ? 'true' : undefined}
              onClick={handleExportCsv}
              className={cn('gap-1.5 min-h-9', exportDisabled && 'opacity-50 cursor-not-allowed')}
            >
              <Download className="h-3.5 w-3.5" aria-hidden="true" />
              Export CSV
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              aria-disabled={exportDisabled ? 'true' : undefined}
              onClick={handleExportJson}
              className={cn('gap-1.5 min-h-9', exportDisabled && 'opacity-50 cursor-not-allowed')}
            >
              <Download className="h-3.5 w-3.5" aria-hidden="true" />
              Export JSON
            </Button>
          </div>
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

      {hasRemoteClusters && (
        <Card>
          <CardHeader className="pb-2">
            <h2 className="text-sm font-semibold leading-none tracking-tight">Federated Cluster Attribution</h2>
          </CardHeader>
          <CardContent className="flex flex-wrap gap-2">
            {byCluster.map((cl) => (
              <div
                key={cl.cluster || 'local'}
                className="inline-flex items-center gap-2 rounded-md border bg-muted/40 px-3 py-1.5 text-xs font-mono"
              >
                <span className="font-semibold">{cl.cluster || 'local'}</span>
                <span className="text-muted-foreground">
                  {cl.activeEnvironments} active · {cl.hibernatedEnvironments} idle
                </span>
                <span>{formatHourlyCostUSD(cl.hourlyCostUsd)}</span>
              </div>
            ))}
          </CardContent>
        </Card>
      )}

      {dailyTrend.length > 0 && (
        <Card>
          <CardHeader className="flex flex-row items-center justify-between gap-4 flex-wrap">
            <div>
              <h2 className="text-base font-semibold leading-none tracking-tight">
                Historical Spend &amp; Savings Trend ({windowDays}d)
              </h2>
              <p className="text-xs text-muted-foreground mt-1">
                Daily UTC accrued preview spend versus hibernation and delta-routing savings, including torn-down PRs
              </p>
            </div>
          </CardHeader>
          <CardContent>
            <figure className="space-y-3">
              <svg
                role="img"
                aria-labelledby={trendTitleId}
                aria-describedby={trendDescId}
                viewBox="0 0 720 160"
                className="w-full h-40 overflow-visible"
              >
                <title id={trendTitleId}>
                  Daily preview spend and savings over the last {windowDays} days
                </title>
                <desc id={trendDescId}>
                  Total accrued spend {formatCurrencyUSD(trendTotals.accrued)} and total savings{' '}
                  {formatCurrencyUSD(trendTotals.saved)} across {dailyTrend.length} UTC days.
                </desc>
                <line x1={0} y1={140} x2={720} y2={140} stroke="currentColor" strokeOpacity={0.15} />
                {dailyTrend.map((pt, idx) => {
                  const slotWidth = 720 / Math.max(dailyTrend.length, 1)
                  const barWidth = Math.max(2, Math.min(12, slotWidth * 0.36))
                  const xBase = idx * slotWidth + (slotWidth - barWidth * 2 - 2) / 2
                  const spendHeight = Math.min(130, (pt.accruedCostUsd / trendTotals.maxDaily) * 130)
                  const daySaved = pt.hibernationSavingsUsd + pt.deltaRoutingSavingsUsd
                  const savedHeight = Math.min(130, (daySaved / trendTotals.maxDaily) * 130)
                  return (
                    <g key={pt.date}>
                      <rect
                        x={xBase}
                        y={140 - spendHeight}
                        width={barWidth}
                        height={Math.max(spendHeight, pt.accruedCostUsd > 0 ? 2 : 0)}
                        rx={1.5}
                        fill="currentColor"
                        className="text-primary/80"
                      />
                      <rect
                        x={xBase + barWidth + 2}
                        y={140 - savedHeight}
                        width={barWidth}
                        height={Math.max(savedHeight, daySaved > 0 ? 2 : 0)}
                        rx={1.5}
                        fill="currentColor"
                        className="text-emerald-500/80"
                      />
                    </g>
                  )
                })}
              </svg>
              <figcaption className="flex items-center justify-between gap-3 flex-wrap text-xs text-muted-foreground">
                <span className="flex items-center gap-4 flex-wrap">
                  <span className="inline-flex items-center gap-1.5">
                    <span className="inline-block h-2.5 w-2.5 rounded-sm bg-primary/80" aria-hidden="true" />
                    Window Accrued Spend: <strong className="font-mono text-foreground">{formatCurrencyUSD(trendTotals.accrued)}</strong>
                  </span>
                  <span className="inline-flex items-center gap-1.5">
                    <span className="inline-block h-2.5 w-2.5 rounded-sm bg-emerald-500/80" aria-hidden="true" />
                    Window Savings: <strong className="font-mono text-emerald-600 dark:text-emerald-400">{formatCurrencyUSD(trendTotals.saved)}</strong>
                  </span>
                </span>
                <span className="font-mono">
                  Closed PRs recorded: {summary?.historicalEnvironments ?? 0} ({formatCurrencyUSD(summary?.historicalAccruedCostUsd ?? 0)} accrued)
                </span>
              </figcaption>
            </figure>

            <details className="mt-3 rounded-md border px-3 py-2 text-xs">
              <summary className="cursor-pointer font-medium select-none py-1">
                View daily spend and savings data table ({dailyTrend.length} days)
              </summary>
              <div className="finops-table-wrapper mt-2">
                <table className="w-full text-xs">
                  <caption id={trendTableCaptionId} className="visually-hidden">
                    Daily UTC preview spend, hibernation and delta routing savings, and environment counts
                  </caption>
                  <thead>
                    <tr className="border-b text-left text-muted-foreground">
                      <th scope="col" className="py-1.5 pr-3 font-medium">Date (UTC)</th>
                      <th scope="col" className="py-1.5 px-3 font-medium">Accrued Spend</th>
                      <th scope="col" className="py-1.5 px-3 font-medium">Savings</th>
                      <th scope="col" className="py-1.5 px-3 font-medium">Active Envs</th>
                      <th scope="col" className="py-1.5 pl-3 font-medium">Closed PRs</th>
                    </tr>
                  </thead>
                  <tbody>
                    {dailyTrend.map((pt) => (
                      <tr key={pt.date} className="border-b last:border-0">
                        <th scope="row" className="py-1.5 pr-3 font-mono font-medium text-left">
                          <time dateTime={pt.date}>{pt.date}</time>
                        </th>
                        <td className="py-1.5 px-3 font-mono">{formatCurrencyUSD(pt.accruedCostUsd)}</td>
                        <td className="py-1.5 px-3 font-mono text-emerald-600 dark:text-emerald-400">
                          {formatCurrencyUSD(pt.hibernationSavingsUsd + pt.deltaRoutingSavingsUsd)}
                        </td>
                        <td className="py-1.5 px-3 font-mono">{pt.activeEnvironments}</td>
                        <td className="py-1.5 pl-3 font-mono">{pt.terminatedEnvironments}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </details>
          </CardContent>
        </Card>
      )}

      {sortedConsumers.length === 0 && byNamespace.length === 0 && (summary?.historicalEnvironments ?? 0) === 0 ? (
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
                        const warnThreshold =
                          ns.alertThresholdPercent > 0 && ns.alertThresholdPercent <= 100
                            ? ns.alertThresholdPercent
                            : 80
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
                                  <div className="flex items-center justify-between gap-2 text-xs font-mono">
                                    <span>{Math.round(pct)}%</span>
                                    <span className="text-muted-foreground">
                                      warn @ {Math.round(warnThreshold)}%
                                      {ns.quota?.quotaExceedPolicy ? ` · ${ns.quota.quotaExceedPolicy}` : ''}
                                    </span>
                                  </div>
                                  <progress
                                    value={Math.min(pct, 100)}
                                    max={100}
                                    aria-labelledby={progLabelId}
                                    className={cn(
                                      'budget-progress',
                                      pct >= 100
                                        ? 'budget-progress--critical'
                                        : pct >= warnThreshold
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
              <div className="flex items-center gap-2 flex-wrap">
                {rateCard && (
                  <div className="text-xs font-mono text-muted-foreground bg-muted px-2.5 py-1 rounded">
                    Rate card: ${rateCard.vcpuHourUsd.toFixed(3)}/vCPU-hr · ${rateCard.memoryGibHourUsd.toFixed(3)}/GiB-hr ·
                    {' '}${rateCard.databaseSchemaHourUsd.toFixed(3)}/schema-hr · ${rateCard.databaseDedicatedHourUsd.toFixed(3)}/DB-hr
                  </div>
                )}
              </div>
            </CardHeader>
            <CardContent>
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
                      <tr
                        key={`${item.namespace}/${item.name}/${item.historical ? item.terminatedAt || 'hist' : 'live'}`}
                        className="border-b last:border-0"
                      >
                        <th scope="row" data-value={item.name} className="py-2.5 px-3 font-medium text-left">
                          {item.historical ? (
                            <span className="font-mono text-muted-foreground">{item.name}</span>
                          ) : (
                            <Link
                              to={`/environments/${item.namespace}/${item.name}`}
                              className="text-primary hover:underline font-mono"
                            >
                              {item.name}
                            </Link>
                          )}
                          {item.historical && (
                            <span className="ml-2 inline-flex items-center rounded-full bg-muted px-2 py-0.5 text-[11px] font-sans font-medium text-muted-foreground">
                              Closed
                            </span>
                          )}
                          {item.cluster && item.cluster !== 'local' && (
                            <span className="ml-2 inline-flex items-center rounded-full bg-sky-500/10 px-2 py-0.5 text-[11px] font-mono font-medium text-sky-700 dark:text-sky-400">
                              {item.cluster}
                            </span>
                          )}
                          {item.costSource === 'opencost' && (
                            <span className="ml-2 inline-flex items-center rounded-full bg-emerald-500/10 px-2 py-0.5 text-[11px] font-sans font-medium text-emerald-700 dark:text-emerald-400">
                              Live
                            </span>
                          )}
                          {item.ttlWarning && (
                            <span
                              className="ml-2 inline-flex items-center rounded-full bg-amber-500/10 px-2 py-0.5 text-[11px] font-sans font-medium text-amber-600 dark:text-amber-400"
                            >
                              TTL Warning: {item.ttlWarningReason}
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
