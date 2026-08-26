import { render, screen, waitFor, fireEvent } from '@/test/utils'
import { server } from '@/test/mocks/server'
import { http, HttpResponse } from 'msw'
import { HooksTab } from './HooksTab'
import { describe, it, expect } from 'vitest'

describe('HooksTab', () => {
  it('renders hook jobs table', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/ListHookJobs', () => {
        return HttpResponse.json({
          jobs: [
            {
              name: 'env-migration-abc',
              type: 'migration',
              phase: 'Succeeded',
              message: '',
              durationSeconds: 8,
            },
            {
              name: 'env-postdeploy-xyz',
              type: 'postdeploy',
              phase: 'Failed',
              message: 'exit code 1: connection refused',
              durationSeconds: 45,
            },
          ],
        })
      })
    )

    render(<HooksTab namespace="default" environmentName="test-env" />)

    expect(await screen.findByText('migration')).toBeInTheDocument()
    expect(screen.getByText('postdeploy')).toBeInTheDocument()
    expect(screen.getByText('Succeeded')).toBeInTheDocument()
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.getByText('8s')).toBeInTheDocument()
    expect(screen.getByText('exit code 1: connection refused')).toBeInTheDocument()
  })

  it('shows retry button for failed hooks', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/ListHookJobs', () => {
        return HttpResponse.json({
          jobs: [
            {
              name: 'env-migration-abc',
              type: 'migration',
              phase: 'Failed',
              message: 'syntax error',
              durationSeconds: 3,
            },
          ],
        })
      }),
      http.post('*/diverge.v1alpha1.EnvironmentService/RetryHook', () => {
        return HttpResponse.json({
          job: { name: 'env-migration-retry', type: 'migration', phase: 'Pending' },
        })
      })
    )

    render(<HooksTab namespace="default" environmentName="test-env" />)

    const retryBtn = await screen.findByRole('button', { name: /retry migration hook/i })
    expect(retryBtn).toBeInTheDocument()

    fireEvent.click(retryBtn)

    await waitFor(() => {
      expect(retryBtn).not.toBeDisabled()
    })
  })

  it('shows empty state when no hooks', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/ListHookJobs', () => {
        return HttpResponse.json({ jobs: [] })
      })
    )

    render(<HooksTab namespace="default" environmentName="test-env" />)

    expect(await screen.findByText('No hooks configured for this environment.')).toBeInTheDocument()
  })

  it('shows error state', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/ListHookJobs', () => {
        return HttpResponse.json(
          { code: 'internal', message: 'database connection failed' },
          { status: 500 }
        )
      })
    )

    render(<HooksTab namespace="default" environmentName="test-env" />)

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })

  it('does not show retry for succeeded hooks', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/ListHookJobs', () => {
        return HttpResponse.json({
          jobs: [
            {
              name: 'env-migration-ok',
              type: 'migration',
              phase: 'Succeeded',
              message: '',
              durationSeconds: 5,
            },
          ],
        })
      })
    )

    render(<HooksTab namespace="default" environmentName="test-env" />)

    await screen.findByText('migration')
    expect(screen.queryByRole('button', { name: /retry/i })).not.toBeInTheDocument()
  })
})
