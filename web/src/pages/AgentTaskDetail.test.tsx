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

  it('resumes a paused task with an optional USD budget bump, supports steering while paused, and rejects negative bumps', async () => {
    let resumeBody: { budgetUsdBump?: string } | null = null
    let guidedMessage = ''
    const pausedTask = {
      task: {
        ...mockActiveTask.task,
        spec: { ...mockActiveTask.task.spec, suspended: true },
        status: { ...mockActiveTask.task.status, phase: 'AGENT_TASK_PHASE_PAUSED', prUrl: 'javascript:alert(1)' },
      },
    }

    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/GetTask', () =>
        HttpResponse.json(pausedTask),
      ),
      http.post('*/diverge.v1alpha1.AgentTaskService/GuideTask', async ({ request }) => {
        const body = (await request.json()) as { message?: string }
        guidedMessage = body.message ?? ''
        return HttpResponse.json({ accepted: true })
      }),
      http.post('*/diverge.v1alpha1.AgentTaskService/ResumeTask', async ({ request }) => {
        resumeBody = (await request.json()) as { budgetUsdBump?: string }
        return HttpResponse.json(mockActiveTask)
      }),
    )

    renderPage()
    const resumeBtn = await screen.findByRole('button', { name: /Resume/i })
    // Unsafe javascript: PR URL must not render an Open PR anchor
    expect(screen.queryByRole('button', { name: /Open PR/i })).not.toBeInTheDocument()
    // Live steering input remains enabled while paused so operators can steer before resuming
    const guidanceInput = screen.getByLabelText('Guidance message')
    expect(guidanceInput).not.toBeDisabled()

    fireEvent.click(resumeBtn)
    const bumpInput = await screen.findByLabelText(/Additional Budget USD/i)

    // Negative bump triggers validation alert
    fireEvent.change(bumpInput, { target: { value: '-5.00' } })
    fireEvent.click(screen.getAllByRole('button', { name: /^Resume$/i, hidden: true }).at(-1)!)
    expect(await screen.findByRole('alert')).toHaveTextContent('Additional budget USD must be a positive number')

    // Valid bump (+$7.50) + optional resume guidance calls GuideTask and ResumeTask RPCs
    fireEvent.click(screen.getByRole('button', { name: /Resume/i }))
    fireEvent.change(await screen.findByLabelText(/Additional Budget USD/i), { target: { value: '+$7.50' } })
    fireEvent.change(await screen.findByLabelText(/Guidance for Resume/i), {
      target: { value: 'Skip e2e suite and focus on unit tests' },
    })
    fireEvent.click(screen.getAllByRole('button', { name: /Guide & Resume/i, hidden: true }).at(-1)!)

    await waitFor(() => {
      expect(guidedMessage).toBe('Skip e2e suite and focus on unit tests')
      expect(resumeBody).toMatchObject({ budgetUsdBump: '7.50' })
      expect(screen.getByRole('status')).toHaveTextContent('Task resumed (+$7.50 budget)')
    })
  })

  it('disables live guidance with a completed placeholder when task is in a terminal phase', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/GetTask', () =>
        HttpResponse.json({
          task: {
            ...mockActiveTask.task,
            status: { ...mockActiveTask.task.status, phase: 'AGENT_TASK_PHASE_COMPLETED' },
          },
        }),
      ),
    )

    renderPage()
    await screen.findByText('fix-checkout')

    const guidanceInput = screen.getByLabelText('Guidance message')
    expect(guidanceInput).toBeDisabled()
    expect(guidanceInput).toHaveAttribute(
      'placeholder',
      'Task has completed and can no longer receive live guidance',
    )
    expect(screen.queryByRole('button', { name: /Pause/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Resume/i })).not.toBeInTheDocument()
  })

  it('deletes a task after confirmation and handles GetTask error retry and YAML tab', async () => {
    let getAttempts = 0
    let deleteCalled = false
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/GetTask', () => {
        getAttempts++
        if (getAttempts === 1) {
          return HttpResponse.json({ code: 'internal', message: 'timeout' }, { status: 500 })
        }
        return HttpResponse.json(mockActiveTask)
      }),
      http.post('*/diverge.v1alpha1.AgentTaskService/DeleteTask', () => {
        deleteCalled = true
        return HttpResponse.json({})
      }),
    )

    renderPage()
    expect(await screen.findByText('Failed to load agent task')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Retry/i }))

    expect(await screen.findByText('fix-checkout')).toBeInTheDocument()

    // Switch to YAML tab
    fireEvent.click(screen.getByRole('tab', { name: /YAML/i }))
    expect(await screen.findByText(/fix-checkout-claim/)).toBeInTheDocument()

    // Click Delete
    fireEvent.click(screen.getByRole('button', { name: /Delete/i }))
    await waitFor(() => {
      expect(deleteCalled).toBe(true)
    })
  })

  it('displays linked preview environment and Open Preview links when a matching environment exists', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/GetTask', () =>
        HttpResponse.json(mockActiveTask),
      ),
      http.post('*/diverge.v1alpha1.EnvironmentService/ListEnvironments', () =>
        HttpResponse.json({
          environments: [
            {
              name: 'agent-preview-fix-checkout',
              namespace: 'default',
              labels: { 'divergedev.com/agent-task': 'fix-checkout' },
              status: {
                phase: 'Ready',
                url: 'https://agent-preview-fix-checkout.preview.example.com',
              },
            },
          ],
        }),
      ),
    )

    renderPage()

    expect(await screen.findByText('fix-checkout')).toBeInTheDocument()
    const envLink = await screen.findByRole('link', { name: 'agent-preview-fix-checkout' })
    expect(envLink).toHaveAttribute('href', '/environments/default/agent-preview-fix-checkout')
    expect(screen.getByText('Linked Preview')).toBeInTheDocument()

    const openPreviewButtons = screen.getAllByRole('link', { name: /Open Preview/i })
    expect(openPreviewButtons.length).toBeGreaterThanOrEqual(1)
    expect(openPreviewButtons[0]).toHaveAttribute(
      'href',
      'https://agent-preview-fix-checkout.preview.example.com',
    )
  })
})
