import { render, screen, waitFor, fireEvent } from '@/test/utils'
import { server } from '@/test/mocks/server'
import { http, HttpResponse } from 'msw'
import { describe, it, expect, vi, afterEach } from 'vitest'
import { Environment } from '@/api/gen/diverge/v1alpha1/environment_pb'
import { environmentClient } from '@/api/client'
import { FlagsTab } from './FlagsTab'

describe('FlagsTab', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders provider info, existing overrides, and injected env vars', () => {
    const env = Environment.fromJson({
      name: 'pr-42',
      namespace: 'default',
      spec: {
        features: {
          provider: 'flipt',
          connectionRef: 'flipt-secret',
          overrides: {
            new_checkout: 'true',
            pricing_tier: 'enterprise',
          },
        },
      },
      status: {
        phase: 'Ready',
        conditions: [
          {
            type: 'FeaturesReady',
            status: 'True',
            reason: 'FliptSynced',
            message: 'Synced 2 flags to Flipt namespace',
          },
        ],
        featureEnvVars: {
          FLIPT_URL: 'http://flipt.default.svc:8080',
          FLIPT_NAMESPACE: 'diverge-pr-42',
          FLIPT_AUTH_TOKEN: 'super-secret-token',
        },
      },
    })

    render(<FlagsTab environment={env} />)

    expect(screen.getByText('Flipt Feature Flags')).toBeInTheDocument()
    expect(screen.getByText('2 configured')).toBeInTheDocument()
    expect(screen.getByText('flipt-secret')).toBeInTheDocument()
    expect(screen.getByText('Synced 2 flags to Flipt namespace')).toBeInTheDocument()

    // Overrides
    expect(screen.getByText('new_checkout')).toBeInTheDocument()
    expect(screen.getByText('pricing_tier')).toBeInTheDocument()
    expect(screen.getByText('enterprise')).toBeInTheDocument()

    // Injected env vars (token should be masked)
    expect(screen.getByText('FLIPT_URL')).toBeInTheDocument()
    expect(screen.getByText('http://flipt.default.svc:8080')).toBeInTheDocument()
    expect(screen.getByText('FLIPT_AUTH_TOKEN')).toBeInTheDocument()
    expect(screen.getByText('••••••••••••••••')).toBeInTheDocument()
  })

  it('adds a new flag override via updateEnvironment with updateMask', async () => {
    const env = Environment.fromJson({
      name: 'pr-42',
      namespace: 'default',
      spec: {
        features: {
          provider: 'flipt',
          overrides: {
            new_checkout: 'true',
          },
        },
      },
    })

    const updateSpy = vi.spyOn(environmentClient, 'updateEnvironment')
    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/UpdateEnvironment', () =>
        HttpResponse.json({ environment: env.toJson() }),
      ),
    )

    render(<FlagsTab environment={env} />)

    fireEvent.change(screen.getByLabelText('Flag key'), {
      target: { value: 'dark_mode' },
    })
    fireEvent.change(screen.getByLabelText('Flag value'), {
      target: { value: 'v2' },
    })
    fireEvent.click(screen.getByRole('button', { name: /Add Override/i }))

    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith({
        environment: expect.objectContaining({
          name: 'pr-42',
          namespace: 'default',
          spec: expect.objectContaining({
            features: expect.objectContaining({
              overrides: {
                new_checkout: 'true',
                dark_mode: 'v2',
              },
            }),
          }),
        }),
        updateMask: { paths: ['spec.features.overrides'] },
      })
      expect(screen.getByRole('status')).toHaveTextContent('Saved override for dark_mode')
    })
  })

  it('toggles a boolean flag override and removes an override', async () => {
    const env = Environment.fromJson({
      name: 'pr-42',
      namespace: 'default',
      spec: {
        features: {
          provider: 'configmap',
          overrides: {
            new_checkout: 'true',
            pricing_tier: 'enterprise',
          },
        },
      },
    })

    const updateSpy = vi.spyOn(environmentClient, 'updateEnvironment')
    let serverEnv = env
    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/UpdateEnvironment', async ({ request }) => {
        const body = (await request.json()) as {
          environment?: { spec?: { features?: { overrides?: Record<string, string> } } }
        }
        serverEnv = Environment.fromJson({
          name: 'pr-42',
          namespace: 'default',
          spec: {
            features: {
              provider: 'configmap',
              overrides: body.environment?.spec?.features?.overrides ?? {},
            },
          },
        })
        return HttpResponse.json({ environment: serverEnv.toJson() })
      }),
    )

    render(<FlagsTab environment={env} />)

    fireEvent.click(screen.getByRole('button', { name: 'Toggle new_checkout' }))

    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith({
        environment: expect.objectContaining({
          spec: expect.objectContaining({
            features: expect.objectContaining({
              overrides: {
                new_checkout: 'false',
                pricing_tier: 'enterprise',
              },
            }),
          }),
        }),
        updateMask: { paths: ['spec.features.overrides'] },
      })
      expect(screen.getByRole('status')).toHaveTextContent('Toggled new_checkout to false')
    })

    updateSpy.mockClear()
    fireEvent.click(screen.getByRole('button', { name: 'Remove pricing_tier' }))

    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith({
        environment: expect.objectContaining({
          spec: expect.objectContaining({
            features: expect.objectContaining({
              overrides: {
                new_checkout: 'false',
              },
            }),
          }),
        }),
        updateMask: { paths: ['spec.features.overrides'] },
      })
      expect(screen.getByRole('status')).toHaveTextContent('Removed override pricing_tier')
    })
  })
})
