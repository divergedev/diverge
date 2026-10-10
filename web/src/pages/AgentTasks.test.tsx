import { screen, waitFor, fireEvent } from '@/test/utils'
import { server } from '@/test/mocks/server'
import { http, HttpResponse } from 'msw'
import { describe, it, expect } from 'vitest'
import AgentTasks from './AgentTasks'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ThemeProvider } from '@/components/ThemeProvider'
import { AuthProvider } from '@/hooks/useAuth'
import { MemoryRouter } from 'react-router-dom'
import { render as rtlRender } from '@testing-library/react'

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return rtlRender(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <ThemeProvider defaultTheme="dark">
          <AuthProvider>
            <AgentTasks />
          </AuthProvider>
        </ThemeProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('AgentTasks', () => {
  it('renders empty state when no tasks exist', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/ListTasks', () =>
        HttpResponse.json({ tasks: [] }),
      ),
    )

    renderPage()

    expect(await screen.findByText('No agent tasks yet')).toBeInTheDocument()
  })

  it('renders task rows with status, iterations, cost, and PR link', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/ListTasks', () =>
        HttpResponse.json({
          tasks: [
            {
              name: 'fix-checkout-bug',
              namespace: 'default',
              spec: {
                objective: 'Fix race condition in checkout service',
                repository: { url: 'https://github.com/org/checkout', baseBranch: 'main' },
                budgetUsd: '10.00',
                maxIterations: 25,
              },
              status: {
                phase: 'AGENT_TASK_PHASE_ACTIVE',
                iteration: 4,
                costUsd: '1.25',
                prUrl: 'https://github.com/org/checkout/pull/42',
              },
            },
          ],
        }),
      ),
    )

    renderPage()

    expect(await screen.findByText('fix-checkout-bug')).toBeInTheDocument()
    expect(screen.getByText('Active')).toBeInTheDocument()
    expect(screen.getByText('Fix race condition in checkout service')).toBeInTheDocument()
    expect(screen.getByText('4 / 25')).toBeInTheDocument()
    expect(screen.getByText('$1.25 / $10.00')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /PR/i })).toHaveAttribute('href', 'https://github.com/org/checkout/pull/42')
  })

  it('filters tasks by search query', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/ListTasks', () =>
        HttpResponse.json({
          tasks: [
            {
              name: 'task-alpha',
              namespace: 'default',
              spec: { objective: 'Add login metric' },
              status: { phase: 'AGENT_TASK_PHASE_ACTIVE' },
            },
            {
              name: 'task-beta',
              namespace: 'default',
              spec: { objective: 'Refactor billing query' },
              status: { phase: 'AGENT_TASK_PHASE_COMPLETED' },
            },
          ],
        }),
      ),
    )

    renderPage()

    expect(await screen.findByText('task-alpha')).toBeInTheDocument()
    expect(screen.getByText('task-beta')).toBeInTheDocument()

    fireEvent.change(screen.getByPlaceholderText('Search tasks...'), {
      target: { value: 'billing' },
    })

    await waitFor(() => {
      expect(screen.queryByText('task-alpha')).not.toBeInTheDocument()
      expect(screen.getByText('task-beta')).toBeInTheDocument()
    })
  })
})
