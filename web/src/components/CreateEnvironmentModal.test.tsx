import { render, screen, waitFor } from '../test/utils'
import { server } from '../test/mocks/server'
import { http, HttpResponse } from 'msw'
import { CreateEnvironmentModal } from './CreateEnvironmentModal'
import userEvent from '@testing-library/user-event'

describe('CreateEnvironmentModal', () => {
  it('Renders form fields', () => {
    render(<CreateEnvironmentModal open={true} onClose={() => {}} />)
    expect(screen.getByPlaceholderText('my-feature')).toBeInTheDocument()
    expect(screen.getByPlaceholderText('default')).toBeInTheDocument()
    expect(screen.getByDisplayValue('24 hours')).toBeInTheDocument() // TTL Select
  })

  it('Validates name format (lowercase alphanumeric)', async () => {
    const user = userEvent.setup()
    render(<CreateEnvironmentModal open={true} onClose={() => {}} />)

    const nameInput = screen.getByPlaceholderText('my-feature')
    await user.type(nameInput, 'Invalid_Name')

    await user.click(screen.getByText('Create'))
    expect(screen.getByText('Name must be lowercase alphanumeric with hyphens')).toBeInTheDocument()
  })

  it('Cancel closes modal', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    render(<CreateEnvironmentModal open={true} onClose={onClose} />)

    await user.click(screen.getByText('Cancel'))
    expect(onClose).toHaveBeenCalled()
  })

  it('Submit calls createEnvironment mutation', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    render(<CreateEnvironmentModal open={true} onClose={onClose} />)

    const nameInput = screen.getByPlaceholderText('my-feature')
    await user.type(nameInput, 'valid-name')

    await user.click(screen.getByText('Create'))

    await waitFor(() => {
      expect(onClose).toHaveBeenCalled()
    }, { timeout: 10000 })
  }, 15000)

  it('Validates remote target cluster requires spoke kubeconfig secret', async () => {
    const user = userEvent.setup()
    render(<CreateEnvironmentModal open={true} onClose={() => {}} />)

    await user.type(screen.getByPlaceholderText('my-feature'), 'fed-preview')
    await user.type(screen.getByPlaceholderText('local (optional spoke)'), 'us-west-burst')
    await user.click(screen.getByText('Create'))

    expect(
      screen.getByText('Spoke kubeconfig secret is required when targeting a remote cluster'),
    ).toBeInTheDocument()
  })

  it('Accepts dotted DNS subdomain secret name and nests environment in CreateEnvironmentRequest', async () => {
    let capturedBody: Record<string, unknown> | undefined
    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/CreateEnvironment', async ({ request }) => {
        capturedBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({
          environment: {
            name: 'fed-dotted',
            namespace: 'default',
            spec: {},
            status: { phase: 'Ready', services: [] },
          },
        })
      }),
    )

    const user = userEvent.setup()
    const onClose = vi.fn()
    render(<CreateEnvironmentModal open={true} onClose={onClose} />)

    await user.type(screen.getByPlaceholderText('my-feature'), 'fed-dotted')
    await user.type(screen.getByPlaceholderText('local (optional spoke)'), 'us-west-burst')
    await user.type(screen.getByPlaceholderText('spoke-kubeconfig'), 'spoke.west.kubeconfig')
    await user.click(screen.getByText('Create'))

    await waitFor(() => {
      expect(onClose).toHaveBeenCalled()
    }, { timeout: 10000 })

    expect(capturedBody).toBeDefined()
    expect(capturedBody).not.toHaveProperty('name')
    expect(capturedBody).not.toHaveProperty('spec')
    expect(capturedBody).toMatchObject({
      namespace: 'default',
      environment: {
        name: 'fed-dotted',
        namespace: 'default',
        spec: {
          deploy: {
            clusterRef: {
              name: 'us-west-burst',
              kubeconfigSecretRef: {
                name: 'spoke.west.kubeconfig',
              },
            },
          },
        },
      },
    })
  }, 15000)
})
