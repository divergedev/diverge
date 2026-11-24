import { render, screen, waitFor } from '../test/utils'
import { server } from '../test/mocks/server'
import { http, HttpResponse } from 'msw'
import userEvent from '@testing-library/user-event'
import { ComposePreviewGroupModal } from './ComposePreviewGroupModal'
import { normalizeGroupSlug, sha256Hex8 } from '../lib/previewgroup'

describe('ComposePreviewGroupModal', () => {
  it('composes a multi-MR PreviewGroup from selected open PR environments and preserves per-service DB by default', async () => {
    let capturedRequest: Record<string, unknown> | undefined

    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/ListEnvironments', () =>
        HttpResponse.json({
          environments: [
            {
              name: 'pr-acme-web-101',
              namespace: 'frontend',
              spec: {
                source: { provider: 'github', project: 'acme/web', mr: 101, branch: 'feat/checkout' },
                serviceConfig: {
                  serviceName: 'web-app',
                  namespace: 'frontend',
                  image: 'ghcr.io/acme/web:pr-101',
                  port: 3000,
                },
              },
              status: { phase: 'Running' },
            },
            {
              name: 'pr-acme-payments-42',
              namespace: 'backend',
              spec: {
                source: { provider: 'github', project: 'acme/payments', mr: 42, branch: 'feat/checkout' },
                database: { mode: 'neon', seedSource: 'main' },
                serviceConfig: {
                  serviceName: 'payments-api',
                  namespace: 'backend',
                  image: 'ghcr.io/acme/payments:pr-42',
                  port: 8080,
                },
              },
              status: { phase: 'Running' },
            },
          ],
        }),
      ),
      http.post('*/diverge.v1alpha1.PreviewGroupService/CreatePreviewGroup', async ({ request }) => {
        capturedRequest = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({
          previewGroup: {
            name: 'feat-checkout',
            namespace: '',
            spec: {},
            status: { phase: 'Running' },
          },
        })
      }),
    )

    const user = userEvent.setup()
    const onClose = vi.fn()
    render(<ComposePreviewGroupModal open={true} onClose={onClose} />)

    const webCheckbox = await screen.findByRole('checkbox', { name: /web-app/i })
    const paymentsCheckbox = await screen.findByRole('checkbox', { name: /payments-api/i })

    await user.click(webCheckbox)
    await user.click(paymentsCheckbox)

    // Group name auto-populated from branch slug "feat/checkout" -> "feat-checkout"
    const groupNameInput = screen.getByLabelText(/Group Name/i)
    expect(groupNameInput).toHaveValue('feat-checkout')

    await user.type(screen.getByLabelText(/Staging Base URL/i), 'https://staging.acme.dev')
    await user.click(screen.getByRole('button', { name: /Compose Preview Group/i }))

    await waitFor(() => {
      expect(onClose).toHaveBeenCalled()
    })

    expect(capturedRequest).toBeDefined()
    const pg = capturedRequest?.previewGroup as {
      name: string
      annotations?: Record<string, string>
      spec: {
        routing: { headerKey: string; headerValue: string; externalUrl: string }
        database?: unknown
        services: Array<{ name: string; image: string; namespace: string; database?: { mode: string } }>
      }
    }
    expect(pg.name).toBe('feat-checkout')
    expect(pg.spec.routing).toMatchObject({
      headerKey: 'x-diverge-env',
      headerValue: 'feat-checkout',
      externalUrl: 'https://staging.acme.dev/?_diverge_env=feat-checkout',
    })
    expect(pg.spec.database).toBeUndefined()
    expect(pg.spec.services).toHaveLength(2)
    expect(pg.spec.services[1].database?.mode).toBe('neon')
    expect(pg.annotations?.['divergedev.com/group-members']).toContain('acme/payments')
  }, 15000)

  it('validates group name and requires at least one service', async () => {
    const user = userEvent.setup()
    render(<ComposePreviewGroupModal open={true} onClose={() => {}} />)

    const nameInput = screen.getByLabelText(/Group Name/i)
    await user.type(nameInput, 'Bad_Group!')
    await user.click(screen.getByRole('button', { name: /Compose Preview Group/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Name must be lowercase alphanumeric with hyphens',
    )

    await user.clear(nameInput)
    await user.type(nameInput, 'valid-group')
    await user.click(screen.getByRole('button', { name: /Compose Preview Group/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Select at least one open PR/MR environment or add a custom service',
    )
  })

  it('paginates ListEnvironments, blocks cross-MR service collisions in join mode, and retries on Aborted conflicts', async () => {
    let updateCalls = 0
    let lastUpdatedPayload: Record<string, unknown> | undefined

    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/ListEnvironments', async ({ request }) => {
        const body = (await request.json()) as { pageToken?: string }
        if (!body.pageToken) {
          return HttpResponse.json({
            nextPageToken: 'page-2',
            environments: [
              {
                name: 'pr-acme-payments-99',
                namespace: 'backend',
                spec: {
                  source: { provider: 'github', project: 'acme/payments', mr: 99, branch: 'feat/other' },
                  serviceConfig: {
                    serviceName: 'payments-api',
                    namespace: 'backend',
                    image: 'ghcr.io/acme/payments:pr-99',
                    port: 8080,
                  },
                },
              },
            ],
          })
        }
        return HttpResponse.json({
          environments: [
            {
              name: 'pr-acme-web-101',
              namespace: 'frontend',
              resourceVersion: 'rv-web-1',
              spec: {
                source: { provider: 'github', project: 'acme/web', mr: 101, branch: 'feat/checkout' },
                database: { mode: 'neon' },
                serviceConfig: {
                  serviceName: 'web-app',
                  namespace: 'frontend',
                  image: 'ghcr.io/acme/web:pr-101',
                  port: 3000,
                },
              },
            },
          ],
        })
      }),
      http.post('*/diverge.v1alpha1.PreviewGroupService/UpdatePreviewGroup', async ({ request }) => {
        updateCalls++
        lastUpdatedPayload = (await request.json()) as Record<string, unknown>
        const nested = lastUpdatedPayload.previewGroup as Record<string, unknown>
        if ((nested.namespace ?? '') !== '') {
          return HttpResponse.json(
            { code: 'permission_denied', message: 'expected implicit cluster scope on update' },
            { status: 403 },
          )
        }
        if (updateCalls === 1) {
          return HttpResponse.json({ code: 'aborted', message: 'resourceVersion conflict' }, { status: 409 })
        }
        return HttpResponse.json({ previewGroup: nested })
      }),
      http.post('*/diverge.v1alpha1.PreviewGroupService/GetPreviewGroup', async ({ request }) => {
        const getReq = (await request.json()) as Record<string, unknown>
        if ((getReq.namespace ?? '') !== '') {
          return HttpResponse.json(
            { code: 'permission_denied', message: 'expected implicit cluster scope on retry get' },
            { status: 403 },
          )
        }
        return HttpResponse.json({
          previewGroup: {
            name: 'feat-checkout',
            namespace: '',
            resourceVersion: '55',
            annotations: {
              'divergedev.com/group-members': JSON.stringify([
                { service: 'payments-api', provider: 'github', project: 'acme/payments', mr: 42 },
                { service: 'worker', provider: 'github', project: 'acme/worker', mr: 77 },
              ]),
            },
            spec: {
              services: [
                {
                  name: 'payments-api',
                  mode: 'image',
                  image: 'ghcr.io/acme/payments:pr-42',
                  namespace: 'backend',
                  database: { mode: 'schema' },
                },
                {
                  name: 'worker',
                  mode: 'image',
                  image: 'ghcr.io/acme/worker:pr-77',
                  namespace: 'jobs',
                  database: { mode: 'fresh' },
                },
              ],
            },
          },
        })
      }),
    )

    const existingGroup = {
      name: 'feat-checkout',
      namespace: '',
      resourceVersion: '50',
      annotations: {
        'divergedev.com/group-members': JSON.stringify([
          { service: 'payments-api', provider: 'github', project: 'acme/payments', mr: 42 },
        ]),
      },
      spec: {
        services: [
          {
            name: 'payments-api',
            mode: 'image',
            image: 'ghcr.io/acme/payments:pr-42',
            namespace: 'backend',
            database: { mode: 'schema' },
          },
        ],
      },
    } as never

    const user = userEvent.setup()
    const onClose = vi.fn()
    render(<ComposePreviewGroupModal open={true} onClose={onClose} existingGroup={existingGroup} />)

    // Both page 1 (payments-api#99) and page 2 (web-app#101) appear!
    const collidingPayments = await screen.findByRole('checkbox', { name: /payments-api/i })
    const webCheckbox = await screen.findByRole('checkbox', { name: /web-app/i })

    // Selecting payments-api from MR #99 collides with MR #42
    await user.click(collidingPayments)
    await user.click(screen.getByRole('button', { name: /Join Services/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Service "payments-api" is already owned by acme/payments#42',
    )

    // Uncheck colliding MR #99, select web-app#101 + shared DB mode, and verify retry on 409 Aborted
    await user.click(collidingPayments)
    await user.click(webCheckbox)
    await user.selectOptions(screen.getByLabelText(/Database Isolation/i), 'neon')
    await user.click(screen.getByRole('button', { name: /Join Services/i }))

    await waitFor(() => {
      expect(onClose).toHaveBeenCalled()
    })
    expect(updateCalls).toBe(2)

    const updatedPg = lastUpdatedPayload?.previewGroup as {
      namespace?: string
      resourceVersion: string
      annotations?: Record<string, string>
      spec: {
        database?: { mode: string }
        services: Array<{ name: string; database?: unknown }>
      }
    }
    expect(updatedPg.namespace ?? '').toBe('')
    expect(updatedPg.resourceVersion).toBe('55')
    expect(updatedPg.spec.database?.mode).toBe('neon')
    // Retry preserves concurrently added service + member AND strips per-service DB across all 3 services
    expect(updatedPg.spec.services.map((s) => s.name)).toEqual(['payments-api', 'worker', 'web-app'])
    expect(updatedPg.spec.services[0].database).toBeUndefined()
    expect(updatedPg.spec.services[1].database).toBeUndefined()
    expect(updatedPg.spec.services[2].database).toBeUndefined()
    const savedMembers = JSON.parse(updatedPg.annotations?.['divergedev.com/group-members'] ?? '[]') as Array<{
      service: string
    }>
    expect(savedMembers.map((m) => m.service)).toEqual(['payments-api', 'worker', 'web-app'])

    // >63-char branch slug normalizes with deterministic 8-char SHA-256 suffix matching Go/CI
    expect(sha256Hex8('abc')).toBe('ba7816bf')
    const longBranch = 'feat/' + 'very-long-multi-mr-checkout-flow-branch-name-segment-'.repeat(2)
    const normalizedLong = normalizeGroupSlug(longBranch)
    expect(normalizedLong.length).toBeLessThanOrEqual(63)
    expect(normalizedLong.endsWith('-' + sha256Hex8(longBranch))).toBe(true)
  }, 15000)
})
