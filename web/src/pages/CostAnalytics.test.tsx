import { render, screen, fireEvent, within } from '@/test/utils'
import { server } from '@/test/mocks/server'
import { http, HttpResponse } from 'msw'
import { describe, it, expect } from 'vitest'
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
    expect(screen.getByText(/AgentTask LLM spend: \$1\.75/)).toBeInTheDocument()
    expect(screen.getByText(/FinOps Budget & Lifecycle Warnings \(2\)/)).toBeInTheDocument()
    expect(screen.getByText('payments-api')).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: 'payments' })).toBeInTheDocument()

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

    // Selector remains mounted so user can recover back to all namespaces
    expect(screen.getByLabelText('Namespace scope')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Namespace scope'), { target: { value: '' } })
    expect(await screen.findByText('No preview usage recorded yet')).toBeInTheDocument()
  })
})
