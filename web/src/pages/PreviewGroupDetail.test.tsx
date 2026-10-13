import { screen, waitFor, fireEvent } from '@/test/utils'
import { server } from '@/test/mocks/server'
import { http, HttpResponse, delay } from 'msw'
import { describe, it, expect, vi, afterEach } from 'vitest'
import PreviewGroupDetail from './PreviewGroupDetail'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ThemeProvider } from '@/components/ThemeProvider'
import { AuthProvider } from '@/hooks/useAuth'
import { MemoryRouter } from 'react-router-dom'
import { render as rtlRender } from '@testing-library/react'

vi.mock('react-router-dom', async (importOriginal) => {
  const mod = await importOriginal<typeof import('react-router-dom')>()
  return {
    ...mod,
    useParams: () => ({ namespace: 'default', name: 'pg-checkout' }),
  }
})

const futureIso = new Date(Date.now() + 2 * 3600 * 1000).toISOString()

const mockPreviewGroup = {
  previewGroup: {
    name: 'pg-checkout',
    namespace: 'default',
    spec: {
      source: {
        provider: 'github',
        project: 'org/checkout-monorepo',
        branch: 'feat/multi-service',
        mr: 108,
      },
      routing: {
        mode: 'header',
        headerKey: 'X-Diverge-Env',
        headerValue: 'pg-checkout',
        externalUrl: 'https://pg-checkout.preview.example.com',
      },
      services: [
        {
          name: 'api-gateway',
          mode: 'full',
        },
        {
          name: 'payments',
          mode: 'selective',
        },
      ],
    },
    status: {
      phase: 'Ready',
      serviceCount: 2,
      expiresAt: futureIso,
      services: [
        {
          name: 'api-gateway',
          environmentName: 'pg-checkout-api-gateway',
          namespace: 'default',
          phase: 'Ready',
          url: 'https://api-gateway.preview.example.com',
          message: 'Deployment ready',
          changedServices: ['api-gateway'],
        },
        {
          name: 'payments',
          environmentName: 'pg-checkout-payments',
          namespace: 'payments-ns',
          phase: 'Provisioning',
          url: 'javascript:alert(1)',
          reason: 'WaitingForPods',
          changedServices: ['payments', 'billing-worker'],
        },
      ],
      conditions: [
        {
          type: 'ServicesReady',
          status: 'True',
          reason: 'AllServicesReady',
          message: 'All child environments reconciled',
        },
      ],
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
            <PreviewGroupDetail />
          </AuthProvider>
        </ThemeProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('PreviewGroupDetail', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows loading spinner while fetching preview group', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.PreviewGroupService/GetPreviewGroup', async () => {
        await delay(100)
        return HttpResponse.json(mockPreviewGroup)
      }),
    )

    const { container } = renderPage()
    expect(container.querySelector('.animate-spin')).toBeInTheDocument()
    expect(await screen.findByText('pg-checkout')).toBeInTheDocument()
  })

  it('renders overview details, routing header, TTL, and child service environment links', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.PreviewGroupService/GetPreviewGroup', () =>
        HttpResponse.json(mockPreviewGroup),
      ),
    )

    renderPage()

    expect(await screen.findByText('pg-checkout')).toBeInTheDocument()
    expect(screen.getByText('org/checkout-monorepo')).toBeInTheDocument()
    expect(screen.getByText('feat/multi-service')).toBeInTheDocument()
    expect(screen.getByText('#108')).toBeInTheDocument()
    expect(screen.getByText('X-Diverge-Env: pg-checkout')).toBeInTheDocument()
    expect(screen.getByText('https://pg-checkout.preview.example.com')).toBeInTheDocument()

    // Child environment links
    const gwLink = screen.getByRole('link', { name: 'pg-checkout-api-gateway' })
    expect(gwLink).toHaveAttribute('href', '/environments/default/pg-checkout-api-gateway')

    const paymentsLink = screen.getByRole('link', { name: 'pg-checkout-payments' })
    expect(paymentsLink).toHaveAttribute('href', '/environments/payments-ns/pg-checkout-payments')

    // Changed services badges & messages
    expect(screen.getByText('billing-worker')).toBeInTheDocument()
    expect(screen.getByText('Deployment ready')).toBeInTheDocument()
    expect(screen.getByText('WaitingForPods')).toBeInTheDocument()

    // Safe service URL is rendered as a link; unsafe javascript: URL is not
    expect(
      screen.getByRole('link', { name: /https:\/\/api-gateway\.preview\.example\.com/i }),
    ).toBeInTheDocument()
    expect(screen.queryByText('javascript:alert(1)')).not.toBeInTheDocument()
  })

  it('handles error state with retry and switches between Conditions and YAML tabs', async () => {
    let attempts = 0
    server.use(
      http.post('*/diverge.v1alpha1.PreviewGroupService/GetPreviewGroup', () => {
        attempts++
        if (attempts === 1) {
          return HttpResponse.json({ code: 'internal', message: 'upstream error' }, { status: 500 })
        }
        return HttpResponse.json(mockPreviewGroup)
      }),
    )

    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent('Failed to load preview group')
    fireEvent.click(screen.getByRole('button', { name: /Retry/i }))

    expect(await screen.findByText('pg-checkout')).toBeInTheDocument()

    // Switch to Conditions tab
    fireEvent.click(screen.getByRole('tab', { name: /Conditions/i }))
    expect(await screen.findByText('ServicesReady')).toBeInTheDocument()
    expect(screen.getByText('AllServicesReady')).toBeInTheDocument()
    expect(screen.getByText('All child environments reconciled')).toBeInTheDocument()

    // Switch to YAML tab
    fireEvent.click(screen.getByRole('tab', { name: /YAML/i }))
    expect(await screen.findByText(/org\/checkout-monorepo/)).toBeInTheDocument()
  })

  it('deletes preview group after confirmation', async () => {
    let deleteCalled = false
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    server.use(
      http.post('*/diverge.v1alpha1.PreviewGroupService/GetPreviewGroup', () =>
        HttpResponse.json(mockPreviewGroup),
      ),
      http.post('*/diverge.v1alpha1.PreviewGroupService/DeletePreviewGroup', () => {
        deleteCalled = true
        return HttpResponse.json({})
      }),
    )

    renderPage()
    expect(await screen.findByText('pg-checkout')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Delete/i }))
    await waitFor(() => {
      expect(deleteCalled).toBe(true)
    })
  })
})
