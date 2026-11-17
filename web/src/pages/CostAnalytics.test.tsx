import { render, screen, fireEvent, within } from '@/test/utils'
import { server } from '@/test/mocks/server'
import { http, HttpResponse } from 'msw'
import { describe, it, expect, vi } from 'vitest'
import * as finopsLib from '@/lib/finops'
import CostAnalytics from './CostAnalytics'

describe('CostAnalytics', () => {
  it('renders fleet burn, hibernation & delta savings, namespace budgets, service breakdown, and sortable PR consumers', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.ClusterService/GetClusterInfo', () =>
        HttpResponse.json({
          controllerVersion: 'v0.24.0',
          environmentCount: 2,
          previewGroupCount: 1,
          controllerHealthy: true,
          namespaces: ['payments', 'checkout'],
        }),
      ),
      http.post('*/diverge.v1alpha1.ClusterService/GetCostSummary', () =>
        HttpResponse.json({
          totalHourlyCostUsd: 0.046,
          monthlyRunRateUsd: 33.58,
          hibernationSavingsHourlyUsd: 0.012,
          deltaRoutingSavingsHourlyUsd: 0.060,
          totalAccruedCostUsd: 0.140,
          agentTaskCostUsd: 1.75,
          totalComputeHours: 5.0,
          totalCpuCores: 0.75,
          totalMemoryGib: 1.5,
          totalEnvironments: 2,
          activeEnvironments: 1,
          hibernatedEnvironments: 1,
          opencostEnabled: true,
          byNamespace: [
            {
              namespace: 'payments',
              totalEnvironments: 1,
              activeEnvironments: 1,
              hibernatedEnvironments: 0,
              hourlyCostUsd: 0.046,
              hibernationSavingsHourlyUsd: 0,
              accruedCostUsd: 0.140,
              computeHours: 5.0,
              cpuCores: 0.75,
              memoryGib: 1.5,
              quota: {
                maxActiveEnvironments: 5,
                maxHourlyCostUsd: 0.050,
                quotaExceedPolicy: 'hibernate-oldest',
              },
              budgetUtilizationPercent: 92,
              alertThresholdPercent: 65,
              budgetWarning: true,
              warningReason: 'Hourly spend at 92% ($0.046 / $0.050/hr)',
            },
          ],
          byService: [
            {
              serviceName: 'payments-api',
              environmentCount: 1,
              activeCount: 1,
              hibernatedCount: 0,
              hourlyCostUsd: 0.046,
              cpuCores: 0.75,
              memoryGib: 1.5,
            },
          ],
          topConsumers: [
            {
              name: 'pr-101',
              namespace: 'payments',
              project: 'divergedev/bank',
              mr: '101',
              branch: 'feat/instant-payouts',
              phase: 'Running',
              hibernated: false,
              hourlyCostUsd: 0.046,
              hibernationSavingsHourlyUsd: 0,
              accruedCostUsd: 0.140,
              projectedTtlCostUsd: 1.104,
              uptimeHours: 2.0,
              cpuCores: 0.75,
              memoryGib: 1.5,
              serviceCount: 2,
              databaseMode: 'schema',
              costSource: 'opencost',
              ttlWarning: false,
              ttlWarningReason: '',
            },
            {
              name: 'pr-88-idle',
              namespace: 'checkout',
              project: 'divergedev/store',
              mr: '88',
              branch: 'chore/cart-copy',
              phase: 'Running',
              hibernated: true,
              hourlyCostUsd: 0,
              hibernationSavingsHourlyUsd: 0.012,
              accruedCostUsd: 0,
              projectedTtlCostUsd: 0,
              uptimeHours: 6.5,
              cpuCores: 0,
              memoryGib: 0,
              serviceCount: 1,
              databaseMode: 'none',
              ttlWarning: true,
              ttlWarningReason: 'No TTL configured (unbounded lifetime)',
            },
          ],
          rateCard: {
            vcpuHourUsd: 0.04,
            memoryGibHourUsd: 0.004,
            databaseSchemaHourUsd: 0.01,
            databaseDedicatedHourUsd: 0.08,
          },
        }),
      ),
    )

    render(<CostAnalytics />)

    expect(await screen.findByRole('heading', { level: 1, name: 'Cost & Usage Analytics' })).toBeInTheDocument()
    expect(await screen.findByText('$0.072/hr')).toBeInTheDocument() // $0.012 hibernation + $0.060 delta savings
    expect(screen.getByText('OpenCost Live')).toBeInTheDocument()
    expect(screen.getByText('Live')).toBeInTheDocument()
    expect(screen.getByText(/warn @ 65% · hibernate-oldest/)).toBeInTheDocument()
    expect(screen.getByText(/AgentTask LLM spend: \$1\.75/)).toBeInTheDocument()
    expect(screen.getByText(/FinOps Budget & Lifecycle Warnings \(2\)/)).toBeInTheDocument()
    expect(screen.getByText('payments-api')).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: 'payments' })).toBeInTheDocument()

    // Verify 1-click CSV & JSON report downloads
    const downloadSpy = vi.spyOn(finopsLib, 'downloadTextFile').mockImplementation(() => {})
    fireEvent.click(screen.getByRole('button', { name: 'Export CSV' }))
    expect(downloadSpy).toHaveBeenCalledWith(
      'diverge-finops-all-namespaces.csv',
      expect.stringContaining('pr-101'),
      'text/csv;charset=utf-8',
    )
    fireEvent.click(screen.getByRole('button', { name: 'Export JSON' }))
    expect(downloadSpy).toHaveBeenCalledWith(
      'diverge-finops-all-namespaces.json',
      expect.stringContaining('"opencostEnabled": true'),
      'application/json;charset=utf-8',
    )
    downloadSpy.mockRestore()

    // Verify DOM row sorting on Uptime header button
    const uptimeSortBtn = screen.getByRole('button', { name: 'Uptime' })
    const consumersTable = uptimeSortBtn.closest('table')!
    fireEvent.click(uptimeSortBtn)
    expect(uptimeSortBtn.closest('th')).toHaveAttribute('aria-sort', 'ascending')
    const ascRowHeaders = within(consumersTable).getAllByRole('rowheader')
    expect(ascRowHeaders[0]).toHaveTextContent('pr-101')
    expect(ascRowHeaders[1]).toHaveTextContent('pr-88-idle')

    fireEvent.click(uptimeSortBtn)
    expect(uptimeSortBtn.closest('th')).toHaveAttribute('aria-sort', 'descending')
    const descRowHeaders = within(consumersTable).getAllByRole('rowheader')
    expect(descRowHeaders[0]).toHaveTextContent('pr-88-idle')
    expect(descRowHeaders[1]).toHaveTextContent('pr-101')
  })

  it('keeps the namespace selector interactive when a scoped cost query fails so users can recover', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.ClusterService/GetClusterInfo', () =>
        HttpResponse.json({
          controllerVersion: 'v0.24.0',
          environmentCount: 1,
          previewGroupCount: 0,
          controllerHealthy: true,
          namespaces: ['payments', 'restricted'],
        }),
      ),
      http.post('*/diverge.v1alpha1.ClusterService/GetCostSummary', async ({ request }) => {
        const body = (await request.json().catch(() => ({}))) as { namespace?: string }
        if (body.namespace === 'restricted') {
          return HttpResponse.json({ code: 'permission_denied', message: 'forbidden namespace' }, { status: 403 })
        }
        return HttpResponse.json({
          totalHourlyCostUsd: 0.012,
          monthlyRunRateUsd: 8.76,
          totalEnvironments: 1,
          activeEnvironments: 1,
          byNamespace: [],
          byService: [],
          topConsumers: [],
        })
      }),
    )

    render(<CostAnalytics />)
    expect(await screen.findByRole('heading', { level: 1, name: 'Cost & Usage Analytics' })).toBeInTheDocument()

    const nsSelect = await screen.findByLabelText('Namespace scope')
    await screen.findByRole('option', { name: 'restricted' })

    fireEvent.change(nsSelect, { target: { value: 'restricted' } })
    expect(await screen.findByRole('alert')).toHaveTextContent(/Failed to load cost analytics/)
    expect(screen.getByRole('button', { name: 'Export CSV' })).toHaveAttribute('aria-disabled', 'true')

    // Selector remains mounted so user can recover back to all namespaces
    expect(screen.getByLabelText('Namespace scope')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Namespace scope'), { target: { value: '' } })
    expect(await screen.findByText('No preview usage recorded yet')).toBeInTheDocument()
  })

  it('keeps historical window and closed-PR filter controls available after request failures so users can restore settings', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.ClusterService/GetClusterInfo', () =>
        HttpResponse.json({
          controllerVersion: 'v0.27.0',
          environmentCount: 1,
          previewGroupCount: 0,
          controllerHealthy: true,
          namespaces: ['payments'],
        }),
      ),
      http.post('*/diverge.v1alpha1.ClusterService/GetCostSummary', async ({ request }) => {
        const body = (await request.json().catch(() => ({}))) as {
          windowDays?: number
          includeHistorical?: boolean
        }
        if (body.windowDays === 7 || body.includeHistorical === true) {
          return HttpResponse.json({ code: 'internal', message: 'ledger configmap read failed' }, { status: 500 })
        }
        return HttpResponse.json({
          totalHourlyCostUsd: 0.024,
          monthlyRunRateUsd: 17.52,
          totalEnvironments: 1,
          activeEnvironments: 1,
          byNamespace: [],
          byService: [],
          dailyTrend: [
            {
              date: '2026-11-17',
              accruedCostUsd: 0.05,
              hibernationSavingsUsd: 0.01,
              deltaRoutingSavingsUsd: 0.02,
              activeEnvironments: 1,
              terminatedEnvironments: 0,
            },
          ],
          topConsumers: [],
        })
      }),
    )

    render(<CostAnalytics />)
    expect(await screen.findByRole('img', { name: /Daily preview spend and savings over the last 30 days/ })).toBeInTheDocument()

    // Selecting 7d triggers an error, but the 7d/30d window group remains rendered so the user can switch back to 30d.
    fireEvent.click(screen.getByRole('button', { name: '7d' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/Failed to load cost analytics/)
    expect(screen.getByRole('group', { name: 'Historical trend window' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '7d' })).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(screen.getByRole('button', { name: '30d' }))
    expect(await screen.findByRole('img', { name: /Daily preview spend and savings over the last 30 days/ })).toBeInTheDocument()

    // Enabling Include closed PRs triggers an error, and the toggle remains mounted to restore includeHistorical=false.
    fireEvent.click(screen.getByRole('button', { name: /Include closed PRs/ }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/Failed to load cost analytics/)
    const closedBtn = screen.getByRole('button', { name: /Include closed PRs/ })
    expect(closedBtn).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(closedBtn)
    expect(await screen.findByRole('img', { name: /Daily preview spend and savings over the last 30 days/ })).toBeInTheDocument()
  })

  it('renders 7d/30d accessible SVG trend chart with semantic data table disclosure, spoke cluster attribution, and closed PR history toggle', async () => {
    const observedRequests: Array<{ windowDays?: number; includeHistorical?: boolean }> = []
    server.use(
      http.post('*/diverge.v1alpha1.ClusterService/GetClusterInfo', () =>
        HttpResponse.json({
          controllerVersion: 'v0.27.0',
          environmentCount: 1,
          previewGroupCount: 0,
          controllerHealthy: true,
          namespaces: ['payments'],
        }),
      ),
      http.post('*/diverge.v1alpha1.ClusterService/GetCostSummary', async ({ request }) => {
        const body = (await request.json().catch(() => ({}))) as {
          windowDays?: number
          includeHistorical?: boolean
        }
        observedRequests.push(body)
        const days = body.windowDays === 7 ? 7 : 30
        const dailyTrend = Array.from({ length: days }, (_, idx) => ({
          date: `2026-11-${String((idx % 28) + 1).padStart(2, '0')}`,
          accruedCostUsd: 0.05,
          hibernationSavingsUsd: 0.02,
          deltaRoutingSavingsUsd: 0.01,
          activeEnvironments: 1,
          terminatedEnvironments: idx === days - 1 ? 1 : 0,
        }))
        return HttpResponse.json({
          totalHourlyCostUsd: 0.024,
          monthlyRunRateUsd: 17.52,
          totalEnvironments: 1,
          activeEnvironments: 1,
          historicalEnvironments: 1,
          historicalAccruedCostUsd: 0.18,
          historicalSavingsUsd: 0.09,
          byNamespace: [],
          byService: [],
          byCluster: [
            {
              cluster: 'us-west-burst',
              totalEnvironments: 1,
              activeEnvironments: 1,
              hibernatedEnvironments: 0,
              hourlyCostUsd: 0.024,
              accruedCostUsd: 0.12,
              cpuCores: 0.5,
              memoryGib: 1.0,
            },
          ],
          dailyTrend,
          topConsumers: [
            {
              name: 'pr-live-200',
              namespace: 'payments',
              mr: '200',
              branch: 'feat/federated',
              phase: 'Running',
              hourlyCostUsd: 0.024,
              accruedCostUsd: 0.12,
              uptimeHours: 5.0,
              serviceCount: 1,
              databaseMode: 'schema',
              cluster: 'us-west-burst',
            },
          ],
          historicalConsumers: body.includeHistorical
            ? [
                {
                  name: 'pr-closed-199',
                  namespace: 'payments',
                  mr: '199',
                  branch: 'feat/merged',
                  phase: 'Terminated',
                  hourlyCostUsd: 0,
                  accruedCostUsd: 0.18,
                  uptimeHours: 7.5,
                  serviceCount: 1,
                  databaseMode: 'schema',
                  cluster: 'us-west-burst',
                  historical: true,
                  terminatedAt: '2026-11-16T12:00:00Z',
                },
              ]
            : [],
        })
      }),
    )

    render(<CostAnalytics />)

    expect(await screen.findByText('Federated Cluster Attribution')).toBeInTheDocument()
    expect(screen.getAllByText('us-west-burst').length).toBeGreaterThanOrEqual(2)

    const chartSvg = await screen.findByRole('img', { name: /Daily preview spend and savings over the last 30 days/ })
    expect(chartSvg).toBeInTheDocument()
    expect(screen.getByText(/View daily spend and savings data table \(30 days\)/)).toBeInTheDocument()

    // Switch window to 7d via aria-pressed toggle button
    const btn7d = screen.getByRole('button', { name: '7d' })
    expect(btn7d).toHaveAttribute('aria-pressed', 'false')
    fireEvent.click(btn7d)
    expect(await screen.findByRole('img', { name: /Daily preview spend and savings over the last 7 days/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '7d' })).toHaveAttribute('aria-pressed', 'true')

    // Toggle Include closed PRs
    const closedToggle = screen.getByRole('button', { name: /Include closed PRs \(1\)/ })
    expect(closedToggle).toHaveAttribute('aria-pressed', 'false')
    fireEvent.click(closedToggle)
    expect(await screen.findByText('pr-closed-199')).toBeInTheDocument()
    expect(screen.getByText('Closed')).toBeInTheDocument()
    expect(observedRequests.some((r) => r.windowDays === 7 && r.includeHistorical === true)).toBe(true)
  })
})
