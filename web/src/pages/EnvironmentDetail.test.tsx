import { render, screen, waitFor, fireEvent } from '@/test/utils'
import { server } from '@/test/mocks/server'
import { http, HttpResponse } from 'msw'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import EnvironmentDetail from './EnvironmentDetail'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ThemeProvider } from '@/components/ThemeProvider'
import { AuthProvider } from '@/hooks/useAuth'
import { MemoryRouter } from 'react-router-dom'
import { render as rtlRender } from '@testing-library/react'

// Mock useParams since the test render wrapper provides MemoryRouter at root "/"
vi.mock('react-router-dom', async (importOriginal) => {
  const mod = await importOriginal<typeof import('react-router-dom')>()
  return {
    ...mod,
    useParams: () => ({ namespace: 'default', name: 'test-env' }),
  }
})

const mockEnv = {
  environment: {
    name: 'test-env',
    namespace: 'default',
    spec: {
      source: { branch: 'feature/test', provider: 'github', project: 'org/repo' },
    },
    status: {
      phase: 'Ready',
      url: 'https://test-env.preview.example.com',
      conditions: [{ type: 'Ready', status: 'True' }],
    },
  },
}

// Use a fresh QueryClient per test to avoid cross-test cache pollution
function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return rtlRender(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <ThemeProvider defaultTheme="dark">
          <AuthProvider>
            <EnvironmentDetail />
          </AuthProvider>
        </ThemeProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('EnvironmentDetail', () => {
  it('renders environment details with status', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/GetEnvironment', () =>
        HttpResponse.json(mockEnv),
      ),
      http.post('*/diverge.v1alpha1.EnvironmentService/ListHookJobs', () =>
        HttpResponse.json({ jobs: [] }),
      ),
    )

    renderPage()

    expect(await screen.findByText('test-env')).toBeInTheDocument()
    // "Ready" appears in both status badge and conditions — use getAllByText
    const readyBadges = screen.getAllByText('Ready')
    expect(readyBadges.length).toBeGreaterThanOrEqual(1)
  })

  it('shows failure banner when hooks have failed', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/GetEnvironment', () =>
        HttpResponse.json(mockEnv),
      ),
      http.post('*/diverge.v1alpha1.EnvironmentService/ListHookJobs', () =>
        HttpResponse.json({
          jobs: [
            { name: 'hook-migration-abc', type: 'migration', phase: 'Failed', message: 'exit 1', durationSeconds: 3 },
          ],
        }),
      ),
    )

    renderPage()

    expect(await screen.findByText(/1 hook failed/i)).toBeInTheDocument()
    expect(screen.getByText(/View Hooks tab/)).toBeInTheDocument()
  })

  it('deep links to hooks tab from failure banner', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/GetEnvironment', () =>
        HttpResponse.json(mockEnv),
      ),
      http.post('*/diverge.v1alpha1.EnvironmentService/ListHookJobs', () =>
        HttpResponse.json({
          jobs: [
            { name: 'hook-migration-abc', type: 'migration', phase: 'Failed', message: 'fail', durationSeconds: 1 },
          ],
        }),
      ),
    )

    renderPage()

    const link = await screen.findByText(/View Hooks tab/)
    fireEvent.click(link)

    await waitFor(() => {
      expect(screen.getByText('Hook Jobs')).toBeInTheDocument()
    })
  })

  it('shows badge count on hooks tab trigger', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/GetEnvironment', () =>
        HttpResponse.json(mockEnv),
      ),
      http.post('*/diverge.v1alpha1.EnvironmentService/ListHookJobs', () =>
        HttpResponse.json({
          jobs: [
            { name: 'hook-a', type: 'migration', phase: 'Failed', message: '', durationSeconds: 0 },
            { name: 'hook-b', type: 'postdeploy', phase: 'Failed', message: '', durationSeconds: 0 },
          ],
        }),
      ),
    )

    renderPage()

    expect(await screen.findByText('2')).toBeInTheDocument()
  })

  it('does not show failure banner when all hooks succeed', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/GetEnvironment', () =>
        HttpResponse.json(mockEnv),
      ),
      http.post('*/diverge.v1alpha1.EnvironmentService/ListHookJobs', () =>
        HttpResponse.json({
          jobs: [
            { name: 'hook-ok', type: 'migration', phase: 'Succeeded', message: '', durationSeconds: 5 },
          ],
        }),
      ),
    )

    renderPage()

    await screen.findByText('test-env')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
