import { render, screen, waitFor } from '../test/utils'
import { server } from '../test/mocks/server'
import { http, HttpResponse } from 'msw'
import userEvent from '@testing-library/user-event'
import { ComposePreviewGroupModal } from './ComposePreviewGroupModal'

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
})
