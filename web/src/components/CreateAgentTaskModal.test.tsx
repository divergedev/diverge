import { render, screen, waitFor, fireEvent } from '@/test/utils'
import { server } from '@/test/mocks/server'
import { http, HttpResponse } from 'msw'
import { describe, it, expect, vi } from 'vitest'
import { CreateAgentTaskModal } from './CreateAgentTaskModal'

describe('CreateAgentTaskModal', () => {
  it('renders all task creation form fields', () => {
    render(<CreateAgentTaskModal open={true} onClose={() => {}} />)

    expect(screen.getByLabelText('Name')).toBeInTheDocument()
    expect(screen.getByLabelText('Namespace')).toBeInTheDocument()
    expect(screen.getByLabelText('Objective')).toBeInTheDocument()
    expect(screen.getByLabelText('Repository URL')).toBeInTheDocument()
    expect(screen.getByLabelText('Base Branch')).toBeInTheDocument()
    expect(screen.getByLabelText('Budget (USD)')).toBeInTheDocument()
    expect(screen.getByLabelText('Max Iterations')).toBeInTheDocument()
    expect(screen.getByLabelText('Open pull request as draft')).toBeChecked()
  })

  it('validates DNS-1123 name and namespace formats and 63-character limit', async () => {
    render(<CreateAgentTaskModal open={true} onClose={() => {}} />)

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Invalid_Name' } })
    fireEvent.change(screen.getByLabelText('Objective'), { target: { value: 'Fix bug' } })
    fireEvent.change(screen.getByLabelText('Repository URL'), { target: { value: 'https://github.com/org/repo' } })
    fireEvent.click(screen.getByText('Create Task'))

    expect(await screen.findByText(/Name must be lowercase alphanumeric with hyphens/)).toBeInTheDocument()

    // >63 characters should also fail DNS-1123 label validation
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'a'.repeat(64) } })
    fireEvent.click(screen.getByText('Create Task'))
    expect(await screen.findByText(/Name must be lowercase alphanumeric with hyphens/)).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'valid-name' } })
    fireEvent.change(screen.getByLabelText('Namespace'), { target: { value: 'Invalid_NS' } })
    fireEvent.click(screen.getByText('Create Task'))

    expect(await screen.findByText(/Namespace must be lowercase alphanumeric with hyphens/)).toBeInTheDocument()
  })

  it('validates repository URL scheme, host/path, and positive budget USD', async () => {
    render(<CreateAgentTaskModal open={true} onClose={() => {}} />)

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'valid-task' } })
    fireEvent.change(screen.getByLabelText('Objective'), { target: { value: 'Fix bug' } })
    fireEvent.change(screen.getByLabelText('Repository URL'), { target: { value: 'ftp://example.com/repo' } })
    fireEvent.click(screen.getByText('Create Task'))

    expect(await screen.findByText('Repository URL must be a valid HTTP(S) or SSH Git URL')).toBeInTheDocument()

    // Malformed HTTP(S) URL without host or path should be rejected
    fireEvent.change(screen.getByLabelText('Repository URL'), { target: { value: 'https://:bad' } })
    fireEvent.click(screen.getByText('Create Task'))
    expect(await screen.findByText('Repository URL must be a valid HTTP(S) or SSH Git URL')).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Repository URL'), { target: { value: 'https://github.com/org/repo' } })
    fireEvent.change(screen.getByLabelText('Budget (USD)'), { target: { value: '-5.00' } })
    fireEvent.click(screen.getByText('Create Task'))

    expect(await screen.findByText('Budget (USD) must be a positive number')).toBeInTheDocument()
  })

  it('submits valid task creation payload and closes modal', async () => {
    let capturedBody: Record<string, unknown> | null = null
    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/CreateTask', async ({ request }) => {
        capturedBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({
          task: {
            name: 'fix-flaky-test',
            namespace: 'staging',
          },
        })
      }),
    )

    const onClose = vi.fn()
    render(<CreateAgentTaskModal open={true} onClose={onClose} />)

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'fix-flaky-test' } })
    fireEvent.change(screen.getByLabelText('Namespace'), { target: { value: 'staging' } })
    fireEvent.change(screen.getByLabelText('Objective'), { target: { value: 'Stabilize checkout unit test' } })
    fireEvent.change(screen.getByLabelText('Repository URL'), { target: { value: 'https://github.com/org/checkout' } })
    fireEvent.change(screen.getByLabelText('Budget (USD)'), { target: { value: '12.50' } })
    fireEvent.change(screen.getByLabelText('Max Iterations'), { target: { value: '15' } })
    fireEvent.click(screen.getByText('Create Task'))

    await waitFor(() => {
      expect(onClose).toHaveBeenCalled()
    })
    expect(capturedBody).toMatchObject({
      name: 'fix-flaky-test',
      namespace: 'staging',
      spec: {
        objective: 'Stabilize checkout unit test',
        repository: {
          url: 'https://github.com/org/checkout',
          baseBranch: 'main',
        },
        budgetUsd: '12.50',
        maxIterations: 15,
        draftPr: true,
      },
    })
  })

  it('displays server error alert when CreateTask RPC fails', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.AgentTaskService/CreateTask', () =>
        HttpResponse.json({ code: 'already_exists', message: 'agent task already exists' }, { status: 409 }),
      ),
    )

    const onClose = vi.fn()
    render(<CreateAgentTaskModal open={true} onClose={onClose} />)

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'dup-task' } })
    fireEvent.change(screen.getByLabelText('Objective'), { target: { value: 'Fix bug' } })
    fireEvent.change(screen.getByLabelText('Repository URL'), { target: { value: 'https://github.com/org/repo' } })
    fireEvent.click(screen.getByText('Create Task'))

    expect(await screen.findByText(/already exists/i)).toBeInTheDocument()
    expect(onClose).not.toHaveBeenCalled()
  })
})
