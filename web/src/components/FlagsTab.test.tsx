import { render, screen, fireEvent } from '@/test/utils'
import { describe, it, expect, vi } from 'vitest'
import { FlagsTab } from './FlagsTab'
import { Environment } from '@/api/gen/diverge/v1alpha1/environment_pb'

describe('FlagsTab', () => {
  it('renders empty state when no overrides are configured', () => {
    const env = new Environment({
      name: 'test-env',
      namespace: 'default',
      spec: {
        features: {
          provider: 'configmap',
          overrides: {},
        },
      },
    })

    render(<FlagsTab environment={env} />)

    expect(screen.getByText('Kubernetes ConfigMap (flagd)')).toBeInTheDocument()
    expect(screen.getByText('No feature flag overrides configured')).toBeInTheDocument()
    expect(screen.getByText(/0 configured/)).toBeInTheDocument()
  })

  it('renders flag overrides table with boolean and variant flags', () => {
    const env = new Environment({
      name: 'preview-pr-12',
      namespace: 'default',
      spec: {
        features: {
          provider: 'configmap',
          overrides: {
            new_nav: 'true',
            dark_mode: 'false',
            tier: 'pro_tier',
          },
        },
      },
      status: {
        conditions: [
          { type: 'FeaturesReady', status: 'True', reason: 'FeaturesProvisioned', message: 'Ready' },
        ],
      },
    })

    render(<FlagsTab environment={env} />)

    expect(screen.getByText('new_nav')).toBeInTheDocument()
    expect(screen.getByText('dark_mode')).toBeInTheDocument()
    expect(screen.getByText('tier')).toBeInTheDocument()
    expect(screen.getByText('true')).toBeInTheDocument()
    expect(screen.getByText('false')).toBeInTheDocument()
    expect(screen.getByText('pro_tier')).toBeInTheDocument()
    expect(screen.getAllByText('Boolean')).toHaveLength(2)
    expect(screen.getByText('Variant')).toBeInTheDocument()
    expect(screen.getByText(/3 configured/)).toBeInTheDocument()
  })

  it('renders Flipt provider details and console link', () => {
    const env = new Environment({
      name: 'flipt-env',
      namespace: 'default',
      spec: {
        features: {
          provider: 'flipt',
          connectionRef: 'flipt-secret',
          overrides: {
            checkout_v2: 'true',
          },
        },
      },
      status: {
        featureEnvVars: {
          FLIPT_URL: 'http://flipt.example.com',
          FLIPT_NAMESPACE: 'diverge-flipt-env',
          FLIPT_AUTH_TOKEN: 'secret-token',
        },
        conditions: [
          { type: 'FeaturesReady', status: 'True', reason: 'FeaturesProvisioned', message: 'Flipt namespace ready' },
        ],
      },
    })

    render(<FlagsTab environment={env} />)

    expect(screen.getByText('Flipt Feature Flags')).toBeInTheDocument()
    expect(screen.getAllByText('diverge-flipt-env').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('flipt-secret')).toBeInTheDocument()
    expect(screen.getByText('Open Flipt Console')).toHaveAttribute(
      'href',
      'http://flipt.example.com/namespaces/diverge-flipt-env'
    )

    // Injected env vars
    expect(screen.getByText('FLIPT_URL')).toBeInTheDocument()
    expect(screen.getByText('http://flipt.example.com')).toBeInTheDocument()
    expect(screen.getByText('FLIPT_AUTH_TOKEN')).toBeInTheDocument()
    // Token is masked
    expect(screen.getByText('••••••••••••••••')).toBeInTheDocument()
  })

  it('copies text to clipboard when copy button is clicked', () => {
    const writeTextMock = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, {
      clipboard: {
        writeText: writeTextMock,
      },
    })

    const env = new Environment({
      name: 'copy-env',
      namespace: 'default',
      spec: {
        features: {
          provider: 'configmap',
          overrides: {
            copy_flag: 'true',
          },
        },
      },
      status: {
        featureEnvVars: {
          FLAGD_PATH: '/etc/flags.json',
        },
      },
    })

    render(<FlagsTab environment={env} />)

    const copyBtn = screen.getByTitle('Copy copy_flag')
    fireEvent.click(copyBtn)
    expect(writeTextMock).toHaveBeenCalledWith('copy_flag')

    const copyEnvBtn = screen.getByTitle('Copy FLAGD_PATH')
    fireEvent.click(copyEnvBtn)
    expect(writeTextMock).toHaveBeenCalledWith('/etc/flags.json')
  })
})
