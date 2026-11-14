import { render, screen, fireEvent } from '@/test/utils'
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
    expect(screen.getByText('$0.072/hr')).toBeInTheDocument() // $0.012 hibernation + $0.060 delta savings
    expect(screen.getByText(/AgentTask LLM spend: \$1\.75/)).toBeInTheDocument()
    expect(screen.getByText(/FinOps Budget & Lifecycle Warnings \(2\)/)).toBeInTheDocument()
    expect(screen.getByText('payments-api')).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: 'payments' })).toBeInTheDocument()

    // Verify DOM row sorting on Uptime header button
    const uptimeSortBtn = screen.getByRole('button', { name: 'Uptime' })
    fireEvent.click(uptimeSortBtn)
    expect(uptimeSortBtn.closest('th')).toHaveAttribute('aria-sort', 'ascending')

    fireEvent.click(uptimeSortBtn)
    expect(uptimeSortBtn.closest('th')).toHaveAttribute('aria-sort', 'descending')
  })
})
