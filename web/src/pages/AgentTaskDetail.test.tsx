import { screen, waitFor, fireEvent } from '@/test/utils'
import { server } from '@/test/mocks/server'
import { http, HttpResponse } from 'msw'
import { describe, it, expect, vi } from 'vitest'
import AgentTaskDetail from './AgentTaskDetail'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ThemeProvider } from '@/components/ThemeProvider'
import { AuthProvider } from '@/hooks/useAuth'
import { MemoryRouter } from 'react-router-dom'
import { render as rtlRender } from '@testing-library/react'

vi.mock('react-router-dom', async (importOriginal) => {
  const mod = await importOriginal<typeof import('react-router-dom')>()
  return {
    ...mod,
    useParams: () => ({ namespace: 'default', name: 'fix-checkout' }),
  }
})

const mockActiveTask = {
  task: {
    name: 'fix-checkout',
    namespace: 'default',
    spec: {
      objective: 'Fix race condition in checkout handler',
      repository: {
        url: 'https://github.com/org/checkout',
        baseBranch: 'main',
        workingBranch: 'agent/fix-checkout',
      },
      budgetUsd: '10.00',
      maxIterations: 20,
    },
    status: {
      phase: 'AGENT_TASK_PHASE_ACTIVE',
      iteration: 3,
      costUsd: '0.85',
      tokensConsumed: '45000',
      sandboxClaimRef: 'fix-checkout-claim',
      sandboxPodName: 'sandbox-pod-xyz',
      prUrl: 'https://github.com/org/checkout/pull/99',
    },
  },
}

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return rtlRender(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <ThemeProvider defaultTheme="dark">
          <AuthProvider>
            <AgentTaskDetail />
          </AuthProvider>
        </ThemeProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('AgentTaskDetail', () => {
  it('renders task overview, budget stats, and action buttons', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/GetTask', () =>
        HttpResponse.json(mockActiveTask),
      ),
    )

    renderPage()

    expect(await screen.findByText('fix-checkout')).toBeInTheDocument()
    expect(screen.getByText('Fix race condition in checkout handler')).toBeInTheDocument()
    expect(screen.getByText('3 / 20')).toBeInTheDocument()
    expect(screen.getByText('$0.85 / $10.00')).toBeInTheDocument()
    expect(screen.getByText('sandbox-pod-xyz')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Pause/i })).toBeInTheDocument()
  })

  it('sends guidance message and shows confirmation banner', async () => {
    let guidedMessage = ''
    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/GetTask', () =>
        HttpResponse.json(mockActiveTask),
      ),
      http.post('*/diverge.v1alpha1.AgentTaskService/GuideTask', async ({ request }) => {
        const body = (await request.json()) as { message?: string }
        guidedMessage = body.message ?? ''
        return HttpResponse.json({ accepted: true })
      }),
    )

    renderPage()
    await screen.findByText('fix-checkout')

    const input = screen.getByLabelText('Guidance message')
    fireEvent.change(input, { target: { value: 'Add a table-driven unit test' } })
    fireEvent.click(screen.getByRole('button', { name: /Guide Agent/i }))

    await waitFor(() => {
      expect(guidedMessage).toBe('Add a table-driven unit test')
      expect(screen.getByRole('status')).toHaveTextContent('Guidance sent to agent sandbox')
    })
  })

  it('pauses an active task when Pause is clicked', async () => {
    let pauseCalled = false
    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/GetTask', () =>
        HttpResponse.json(mockActiveTask),
      ),
      http.post('*/diverge.v1alpha1.AgentTaskService/PauseTask', () => {
        pauseCalled = true
        return HttpResponse.json({ task: mockActiveTask.task })
      }),
    )

    renderPage()
    const pauseBtn = await screen.findByRole('button', { name: /Pause/i })
    fireEvent.click(pauseBtn)

    await waitFor(() => {
      expect(pauseCalled).toBe(true)
      expect(screen.getByRole('status')).toHaveTextContent('Task paused')
    })
  })
})
