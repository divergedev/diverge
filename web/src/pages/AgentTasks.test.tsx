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

  it('renders error alert and retry button when listTasks fails', async () => {
    let attempts = 0
    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/ListTasks', () => {
        attempts++
        if (attempts === 1) {
          return HttpResponse.json({ code: 'internal', message: 'backend unavailable' }, { status: 500 })
        }
        return HttpResponse.json({
          tasks: [{ name: 'recovered-task', namespace: 'default', status: { phase: 'AGENT_TASK_PHASE_ACTIVE' } }],
        })
      }),
    )

    renderPage()

    expect(await screen.findByText('Failed to load agent tasks')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Retry/i }))

    expect(await screen.findByText('recovered-task')).toBeInTheDocument()
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

  it('loads subsequent pages and filters tasks across all loaded pages', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/ListTasks', async ({ request }) => {
        const body = (await request.json()) as { pagination?: { pageToken?: string } }
        const token = body.pagination?.pageToken ?? ''
        if (!token) {
          return HttpResponse.json({
            tasks: [
              {
                name: 'task-page-1',
                namespace: 'default',
                spec: { objective: 'Add login metric' },
                status: { phase: 'AGENT_TASK_PHASE_ACTIVE' },
              },
            ],
            pagination: { nextPageToken: 'page-2' },
          })
        }
        return HttpResponse.json({
          tasks: [
            {
              name: 'task-page-2',
              namespace: 'default',
              spec: { objective: 'Refactor billing query' },
              status: { phase: 'AGENT_TASK_PHASE_COMPLETED' },
            },
          ],
          pagination: { nextPageToken: '' },
        })
      }),
    )

    renderPage()

    expect(await screen.findByText('task-page-1')).toBeInTheDocument()
    expect(screen.getByText('task-page-2')).toBeInTheDocument()

    fireEvent.change(screen.getByPlaceholderText('Search tasks...'), {
      target: { value: 'billing' },
    })

    await waitFor(() => {
      expect(screen.queryByText('task-page-1')).not.toBeInTheDocument()
      expect(screen.getByText('task-page-2')).toBeInTheDocument()
    })
  })

  it('paginates when more than 25 tasks exist and blocks unsafe javascript: PR URLs', async () => {
    const manyTasks = Array.from({ length: 28 }, (_, i) => ({
      name: `task-${i + 1}`,
      namespace: 'default',
      spec: { objective: `Objective ${i + 1}` },
      status: {
        phase: 'AGENT_TASK_PHASE_ACTIVE',
        prUrl: i === 0 ? 'javascript:alert(1)' : '',
      },
    }))

    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/ListTasks', () =>
        HttpResponse.json({ tasks: manyTasks }),
      ),
    )

    renderPage()

    expect(await screen.findByText('task-1')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /PR/i })).not.toBeInTheDocument()
    expect(screen.getByText('Page 1 of 2 (28 tasks)')).toBeInTheDocument()
    expect(screen.queryByText('task-26')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Next/i }))
    expect(await screen.findByText('task-26')).toBeInTheDocument()
    expect(screen.getByText('Page 2 of 2 (28 tasks)')).toBeInTheDocument()
  })
})
