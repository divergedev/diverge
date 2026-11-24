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
import { previewGroupClient } from '@/api/client'
import { queryKeys, useListPreviewGroups } from '@/api/queries'
import { Code, ConnectError } from '@connectrpc/connect'
import { useWatchPreviewGroups } from '@/hooks/useWatch'
import { WatchEventType } from '@/api/gen/diverge/v1alpha1/types_pb'

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

  it('renders federated spoke target cluster for PreviewGroup and services', async () => {
    server.use(
      http.post('*/diverge.v1alpha1.PreviewGroupService/GetPreviewGroup', () =>
        HttpResponse.json({
          previewGroup: {
            ...mockPreviewGroup.previewGroup,
            spec: {
              ...mockPreviewGroup.previewGroup.spec,
              clusterRef: {
                name: 'eu-central-spoke',
                kubeconfigSecretRef: { name: 'eu-spoke-kubeconfig' },
              },
              services: [
                mockPreviewGroup.previewGroup.spec.services[0],
                {
                  ...mockPreviewGroup.previewGroup.spec.services[1],
                  clusterRef: {},
                },
              ],
            },
            status: {
              ...mockPreviewGroup.previewGroup.status,
              services: [
                {
                  ...mockPreviewGroup.previewGroup.status.services[0],
                  targetCluster: 'eu-central-spoke',
                },
                {
                  ...mockPreviewGroup.previewGroup.status.services[1],
                  phase: 'Failed',
                  reason: 'CreateFailed',
                },
              ],
            },
          },
        }),
      ),
    )

    renderPage()

    expect(await screen.findByText('Target Cluster')).toBeInTheDocument()
    expect(screen.getAllByText('eu-central-spoke')).toHaveLength(2)
    expect(screen.getByText('local')).toBeInTheDocument()
  })

  it('displays Source MR badges and detaches an individual MR service without deleting remaining services', async () => {
    let updatedPayload: Record<string, unknown> | undefined

    server.use(
      http.post('*/diverge.v1alpha1.PreviewGroupService/GetPreviewGroup', () =>
        HttpResponse.json({
          previewGroup: {
            ...mockPreviewGroup.previewGroup,
            resourceVersion: '42',
            annotations: {
              'divergedev.com/group-members': JSON.stringify([
                { service: 'api-gateway', project: 'acme/gateway', mr: 101, branch: 'feat/checkout' },
                { service: 'payments', project: 'acme/payments', mr: 42, branch: 'feat/checkout' },
              ]),
            },
          },
        }),
      ),
      http.post('*/diverge.v1alpha1.PreviewGroupService/UpdatePreviewGroup', async ({ request }) => {
        updatedPayload = (await request.json()) as Record<string, unknown>
        const nested = updatedPayload.previewGroup as Record<string, unknown>
        return HttpResponse.json({ previewGroup: nested })
      }),
    )

    renderPage()

    expect(await screen.findByText('acme/gateway#101 · feat/checkout')).toBeInTheDocument()
    expect(screen.getByText('acme/payments#42 · feat/checkout')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Detach payments' }))

    await waitFor(() => {
      expect(updatedPayload).toBeDefined()
    })

    const pg = updatedPayload?.previewGroup as {
      annotations: Record<string, string>
      spec: { services: Array<{ name: string }> }
    }
    expect(pg.spec.services).toHaveLength(1)
    expect(pg.spec.services[0].name).toBe('api-gateway')
    expect(pg.annotations['divergedev.com/group-members']).not.toContain('acme/payments')
    expect(pg.annotations['divergedev.com/group-members']).toContain('acme/gateway')
  })

  it('sends If-Match header on final-service detach and refreshes on concurrent join conflict', async () => {
    let seenIfMatchHeader: string | null = null
    let getCalls = 0
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    server.use(
      http.post('*/diverge.v1alpha1.PreviewGroupService/GetPreviewGroup', () => {
        getCalls++
        if (getCalls === 1) {
          return HttpResponse.json({
            previewGroup: {
              ...mockPreviewGroup.previewGroup,
              resourceVersion: '77',
              spec: {
                ...mockPreviewGroup.previewGroup.spec,
                services: [{ name: 'api-gateway', mode: 'image' }],
              },
              status: {
                ...mockPreviewGroup.previewGroup.status,
                services: [],
              },
            },
          })
        }
        return HttpResponse.json({
          previewGroup: {
            ...mockPreviewGroup.previewGroup,
            resourceVersion: '78',
            spec: {
              ...mockPreviewGroup.previewGroup.spec,
              services: [
                { name: 'api-gateway', mode: 'image' },
                { name: 'newly-joined-web', mode: 'image' },
              ],
            },
            status: {
              ...mockPreviewGroup.previewGroup.status,
              services: [],
            },
          },
        })
      }),
      http.post('*/diverge.v1alpha1.PreviewGroupService/DeletePreviewGroup', ({ request }) => {
        seenIfMatchHeader = request.headers.get('If-Match')
        return HttpResponse.json({ code: 'aborted', message: 'Precondition failed' }, { status: 409 })
      }),
    )

    renderPage()

    // Newly joined spec service renders with Detach button even when status.services is empty
    const detachBtn = await screen.findByRole('button', { name: 'Detach api-gateway' })
    fireEvent.click(detachBtn)

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Preview group changed while detaching',
    )
    expect(seenIfMatchHeader).toBe('77')
    expect(await screen.findByText('newly-joined-web')).toBeInTheDocument()
  })

  it('clears spec.source to local when creator MR leaves and only custom local service remains', async () => {
    let updatedPayload: Record<string, unknown> | undefined

    server.use(
      http.post('*/diverge.v1alpha1.PreviewGroupService/GetPreviewGroup', () =>
        HttpResponse.json({
          previewGroup: {
            ...mockPreviewGroup.previewGroup,
            resourceVersion: '60',
            annotations: {
              'divergedev.com/group-members': JSON.stringify([
                {
                  service: 'api-gateway',
                  provider: 'github',
                  project: 'org/checkout-monorepo',
                  mr: 108,
                  branch: 'feat/multi-service',
                },
                { service: 'custom-ui', namespace: 'default', provider: 'local' },
              ]),
            },
            spec: {
              ...mockPreviewGroup.previewGroup.spec,
              services: [
                { name: 'api-gateway', mode: 'image', namespace: 'default', image: 'img/gw:108' },
                { name: 'custom-ui', mode: 'image', namespace: 'default', image: 'img/ui:local' },
              ],
            },
          },
        }),
      ),
      http.post('*/diverge.v1alpha1.PreviewGroupService/UpdatePreviewGroup', async ({ request }) => {
        updatedPayload = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ previewGroup: updatedPayload.previewGroup })
      }),
    )

    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Detach api-gateway' }))

    await waitFor(() => {
      expect(updatedPayload).toBeDefined()
    })
    const sent = updatedPayload?.previewGroup as {
      spec: { source: { provider: string; project?: string; mr?: number; branch: string } }
    }
    expect(sent.spec.source).toEqual({
      provider: 'local',
      branch: 'pg-checkout',
    })
  })

  it('preserves spec.source when source has blank project and matching-provider MR member survives', async () => {
    let updatedPayload: Record<string, unknown> | undefined

    server.use(
      http.post('*/diverge.v1alpha1.PreviewGroupService/GetPreviewGroup', () =>
        HttpResponse.json({
          previewGroup: {
            ...mockPreviewGroup.previewGroup,
            resourceVersion: '61',
            annotations: {
              'divergedev.com/group-members': JSON.stringify([
                {
                  service: 'api-gateway',
                  provider: 'github',
                  project: 'org/checkout-monorepo',
                  mr: 108,
                  branch: 'feat/multi-service',
                },
                {
                  service: 'payments',
                  provider: 'github',
                  project: 'org/payments',
                  mr: 204,
                  branch: 'feat/pay',
                },
              ]),
            },
            spec: {
              ...mockPreviewGroup.previewGroup.spec,
              source: {
                provider: 'github',
                project: '',
                mr: 108,
                branch: 'feat/multi-service',
              },
              services: [
                { name: 'api-gateway', mode: 'image', namespace: 'default', image: 'img/gw:108' },
                { name: 'payments', mode: 'image', namespace: 'default', image: 'img/pay:204' },
              ],
            },
          },
        }),
      ),
      http.post('*/diverge.v1alpha1.PreviewGroupService/UpdatePreviewGroup', async ({ request }) => {
        updatedPayload = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ previewGroup: updatedPayload.previewGroup })
      }),
    )

    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Detach payments' }))

    await waitFor(() => {
      expect(updatedPayload).toBeDefined()
    })
    const sent = updatedPayload?.previewGroup as {
      spec: { source: { provider: string; project?: string; mr?: number; branch: string } }
    }
    expect(sent.spec.source).toEqual({
      provider: 'github',
      mr: 108,
      branch: 'feat/multi-service',
    })
  })

  it('useWatchPreviewGroups ignores older watch snapshots when cache already holds a newer resourceVersion', async () => {
    localStorage.setItem('diverge:token', 'test-token')
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    queryClient.setQueryData(queryKeys.previewGroup('', 'pg-checkout'), {
      previewGroup: {
        name: 'pg-checkout',
        namespace: '',
        resourceVersion: '20',
        spec: { services: [{ name: 'api-gateway' }, { name: 'payments' }] },
      },
    })
    queryClient.setQueryData(queryKeys.previewGroup('', 'pg-barrier'), {
      previewGroup: {
        name: 'pg-barrier',
        namespace: '',
        resourceVersion: '1',
        spec: { services: [] },
      },
    })

    let releaseNewer!: () => void
    const newerGate = new Promise<void>((resolve) => {
      releaseNewer = resolve
    })

    const spy = vi.spyOn(previewGroupClient, 'watchPreviewGroups').mockImplementation(async function* (_req, opts) {
      yield {
        type: WatchEventType.MODIFIED,
        resourceVersion: '15',
        previewGroup: {
          name: 'pg-checkout',
          namespace: '',
          resourceVersion: '15',
          spec: { services: [{ name: 'api-gateway' }] },
        } as never,
      } as never
      yield {
        type: WatchEventType.DELETED,
        resourceVersion: '18',
        previewGroup: {
          name: 'pg-checkout',
          namespace: '',
          resourceVersion: '15',
          spec: { services: [{ name: 'api-gateway' }] },
        } as never,
      } as never
      yield {
        type: WatchEventType.MODIFIED,
        resourceVersion: '2',
        previewGroup: {
          name: 'pg-barrier',
          namespace: '',
          resourceVersion: '2',
          spec: { services: [] },
        } as never,
      } as never
      await newerGate
      yield {
        type: WatchEventType.MODIFIED,
        resourceVersion: '25',
        previewGroup: {
          name: 'pg-checkout',
          namespace: '',
          resourceVersion: '25',
          spec: { services: [{ name: 'api-gateway' }, { name: 'payments' }, { name: 'billing' }] },
        } as never,
      } as never
      yield {
        type: WatchEventType.MODIFIED,
        resourceVersion: '3',
        previewGroup: {
          name: 'pg-barrier',
          namespace: '',
          resourceVersion: '3',
          spec: { services: [] },
        } as never,
      } as never
      await new Promise<void>((resolve) => {
        if (opts?.signal?.aborted) {
          resolve()
          return
        }
        opts?.signal?.addEventListener('abort', () => resolve(), { once: true })
      })
    })

    function WatchHarness() {
      useWatchPreviewGroups()
      return null
    }

    const readCached = (name: string) =>
      queryClient.getQueryData<{ previewGroup?: { resourceVersion?: string; spec?: { services?: unknown[] } } }>(
        queryKeys.previewGroup('', name),
      )

    let unmount: (() => void) | undefined
    try {
      const rendered = rtlRender(
        <QueryClientProvider client={queryClient}>
          <WatchHarness />
        </QueryClientProvider>,
      )
      unmount = rendered.unmount

      await waitFor(() => {
        expect(spy).toHaveBeenCalled()
        expect(readCached('pg-barrier')?.previewGroup?.resourceVersion).toBe('2')
      })
      const afterStale = readCached('pg-checkout')
      expect(afterStale?.previewGroup?.resourceVersion).toBe('20')
      expect(afterStale?.previewGroup?.spec?.services).toHaveLength(2)
      expect(queryClient.getQueryState(queryKeys.previewGroup('', 'pg-checkout'))?.isInvalidated).toBe(true)

      releaseNewer()
      await waitFor(() => {
        expect(readCached('pg-barrier')?.previewGroup?.resourceVersion).toBe('3')
      })
      const afterNewer = readCached('pg-checkout')
      expect(afterNewer?.previewGroup?.resourceVersion).toBe('25')
      expect(afterNewer?.previewGroup?.spec?.services).toHaveLength(3)
    } finally {
      unmount?.()
      spy.mockRestore()
      localStorage.removeItem('diverge:token')
    }
  })

  it('useWatchPreviewGroups invalidates stale list and detail caches on DELETED', async () => {
    localStorage.setItem('diverge:token', 'test-token')
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    queryClient.setQueryData(queryKeys.previewGroups(undefined), {
      previewGroups: [
        {
          name: 'pg-checkout',
          namespace: '',
          resourceVersion: '20',
          spec: { services: [{ name: 'api-gateway' }, { name: 'payments' }] },
        },
      ],
    })
    queryClient.setQueryData(queryKeys.previewGroup('', 'pg-checkout'), {
      previewGroup: {
        name: 'pg-checkout',
        namespace: '',
        resourceVersion: '20',
        spec: { services: [{ name: 'api-gateway' }, { name: 'payments' }] },
      },
    })

    const spy = vi.spyOn(previewGroupClient, 'watchPreviewGroups').mockImplementation(async function* (_req, opts) {
      yield {
        type: WatchEventType.DELETED,
        resourceVersion: '18',
        previewGroup: {
          name: 'pg-checkout',
          namespace: '',
          resourceVersion: '18',
          spec: { services: [] },
        } as never,
      } as never
      await new Promise<void>((resolve) => {
        if (opts?.signal?.aborted) {
          resolve()
          return
        }
        opts?.signal?.addEventListener('abort', () => resolve(), { once: true })
      })
    })

    function WatchHarness() {
      useWatchPreviewGroups()
      return null
    }

    let unmount: (() => void) | undefined
    try {
      const rendered = rtlRender(
        <QueryClientProvider client={queryClient}>
          <WatchHarness />
        </QueryClientProvider>,
      )
      unmount = rendered.unmount

      await waitFor(() => {
        expect(queryClient.getQueryState(queryKeys.previewGroups(undefined))?.isInvalidated).toBe(true)
        expect(queryClient.getQueryState(queryKeys.previewGroup('', 'pg-checkout'))?.isInvalidated).toBe(true)
      })
      const listData = queryClient.getQueryData<{
        previewGroups: Array<{ name: string; resourceVersion: string }>
      }>(queryKeys.previewGroups(undefined))
      expect(listData?.previewGroups.find((p) => p.name === 'pg-checkout')?.resourceVersion).toBe('20')
      const detailData = queryClient.getQueryData<{
        previewGroup?: { resourceVersion: string }
      }>(queryKeys.previewGroup('', 'pg-checkout'))
      expect(detailData?.previewGroup?.resourceVersion).toBe('20')
    } finally {
      unmount?.()
      spy.mockRestore()
      localStorage.removeItem('diverge:token')
    }
  })

  it('useWatchPreviewGroups reconciles active list and detail queries on stale DELETED', async () => {
    const scenarios = ['absent', 'replaced'] as const
    for (const scenario of scenarios) {
      localStorage.setItem('diverge:token', 'test-token')
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, staleTime: 60_000 } },
      })
      const baseGroup = {
        ...mockPreviewGroup.previewGroup,
        resourceVersion: '20',
      }
      queryClient.setQueryData(queryKeys.previewGroups('default'), {
        previewGroups: [baseGroup],
      })
      queryClient.setQueryData(queryKeys.previewGroup('default', 'pg-checkout'), {
        previewGroup: baseGroup,
      })

      let releaseDelete!: () => void
      const deleteGate = new Promise<void>((resolve) => {
        releaseDelete = resolve
      })

      const replacementGroup = {
        ...mockPreviewGroup.previewGroup,
        resourceVersion: '25',
        spec: {
          ...mockPreviewGroup.previewGroup.spec,
          services: [
            ...mockPreviewGroup.previewGroup.spec.services,
            { name: 'billing', mode: 'full' },
          ],
        },
        status: {
          ...mockPreviewGroup.previewGroup.status,
          serviceCount: 3,
          services: [
            ...mockPreviewGroup.previewGroup.status.services,
            {
              name: 'billing',
              environmentName: 'pg-checkout-billing',
              namespace: 'default',
              phase: 'Ready',
              url: 'https://billing.preview.example.com',
            },
          ],
        },
      }

      const listSpy = vi
        .spyOn(previewGroupClient, 'listPreviewGroups')
        .mockImplementation(async () =>
          scenario === 'absent'
            ? ({ previewGroups: [] } as never)
            : ({ previewGroups: [replacementGroup] } as never),
        )
      const getSpy = vi
        .spyOn(previewGroupClient, 'getPreviewGroup')
        .mockImplementation(async () => {
          if (scenario === 'absent') {
            throw new ConnectError('not found', Code.NotFound)
          }
          return { previewGroup: replacementGroup } as never
        })
      const watchSpy = vi
        .spyOn(previewGroupClient, 'watchPreviewGroups')
        .mockImplementation(async function* (_req, opts) {
          await deleteGate
          yield {
            type: WatchEventType.DELETED,
            resourceVersion: '18',
            previewGroup: {
              name: 'pg-checkout',
              namespace: 'default',
              resourceVersion: '18',
              spec: { services: [] },
            } as never,
          } as never
          await new Promise<void>((resolve) => {
            if (opts?.signal?.aborted) {
              resolve()
              return
            }
            opts?.signal?.addEventListener('abort', () => resolve(), { once: true })
          })
        })

      function ActiveReconcileHarness() {
        useWatchPreviewGroups('default')
        const listQuery = useListPreviewGroups({ namespace: 'default' })
        return (
          <>
            <ul data-testid="active-pg-list">
              {(listQuery.data?.previewGroups ?? []).map((p) => (
                <li key={p.name}>{p.name}</li>
              ))}
            </ul>
            <PreviewGroupDetail />
          </>
        )
      }

      let unmount: (() => void) | undefined
      try {
        const rendered = rtlRender(
          <MemoryRouter>
            <QueryClientProvider client={queryClient}>
              <ThemeProvider defaultTheme="dark">
                <AuthProvider>
                  <ActiveReconcileHarness />
                </AuthProvider>
              </ThemeProvider>
            </QueryClientProvider>
          </MemoryRouter>,
        )
        unmount = rendered.unmount

        expect(screen.getByTestId('active-pg-list')).toHaveTextContent('pg-checkout')
        expect(screen.getByRole('heading', { name: 'pg-checkout' })).toBeInTheDocument()

        releaseDelete()

        if (scenario === 'absent') {
          await waitFor(() => {
            expect(listSpy).toHaveBeenCalled()
            expect(getSpy).toHaveBeenCalled()
            expect(screen.getByTestId('active-pg-list').textContent).toBe('')
            expect(screen.getByText('Failed to load preview group')).toBeInTheDocument()
          })
          expect(screen.queryByRole('heading', { name: 'pg-checkout' })).not.toBeInTheDocument()
        } else {
          await waitFor(() => {
            expect(listSpy).toHaveBeenCalled()
            expect(getSpy).toHaveBeenCalled()
            expect(screen.getByText('pg-checkout-billing')).toBeInTheDocument()
          })
          expect(screen.getByTestId('active-pg-list')).toHaveTextContent('pg-checkout')
          expect(screen.getByRole('heading', { name: 'pg-checkout' })).toBeInTheDocument()
          const updatedList = queryClient.getQueryData<{
            previewGroups: Array<{ resourceVersion?: string }>
          }>(queryKeys.previewGroups('default'))
          expect(updatedList?.previewGroups[0]?.resourceVersion).toBe('25')
        }
      } finally {
        unmount?.()
        watchSpy.mockRestore()
        listSpy.mockRestore()
        getSpy.mockRestore()
        localStorage.removeItem('diverge:token')
      }
    }
  })
})
