import { render, screen, waitFor, fireEvent } from '../test/utils'
import Login from './Login'
import { server } from '../test/mocks/server'
import { http, HttpResponse } from 'msw'

describe('Login', () => {
  it('Renders token input and connect button', () => {
    render(<Login />)
    expect(screen.getByPlaceholderText('Paste your token from: kubectl create token diverge-dashboard')).toBeInTheDocument()
    expect(screen.getByText('Connect with Token')).toBeInTheDocument()
  })

  it('Shows disabled SSO button when OIDC is not configured', () => {
    render(<Login />)
    expect(screen.getByText('SSO Login (Not Configured)')).toBeInTheDocument()
    expect(screen.getByText('SSO Login (Not Configured)').closest('button')).toBeDisabled()
  })

  it('Shows or divider between SSO and token sections', () => {
    render(<Login />)
    expect(screen.getByText('or use a token')).toBeInTheDocument()
  })

  it('Shows enabled SSO button when OIDC is configured', async () => {
    server.use(
      http.get('*/auth/config', () => {
        return HttpResponse.json({ oidcEnabled: true, providerName: 'Okta', loginUrl: '/auth/login' })
      })
    )
    render(<Login />)
    const btn = await screen.findByText('Sign in with Okta')
    expect(btn).toBeInTheDocument()
    expect(btn.closest('button')).not.toBeDisabled()
  })

  it('SSO button click redirects to auth/login with return_url', async () => {
    server.use(
      http.get('*/auth/config', () => {
        return HttpResponse.json({ oidcEnabled: true, providerName: 'Okta', loginUrl: '/auth/login' })
      })
    )
    const originalLocation = window.location
    Object.defineProperty(window, 'location', {
      writable: true,
      value: {
        ...originalLocation,
        origin: 'http://localhost:3000',
        protocol: 'http:',
        host: 'localhost:3000',
        hostname: 'localhost',
        port: '3000',
        pathname: '/',
        search: '',
        hash: '',
        href: 'http://localhost:3000/',
        assign: vi.fn(),
        replace: vi.fn(),
      },
    })

    render(<Login />)
    const btn = await screen.findByText('Sign in with Okta')
    fireEvent.click(btn)

    expect((window.location.href as string).endsWith('/auth/login?return_url=%2F')).toBe(true)
    Object.defineProperty(window, 'location', { writable: true, value: originalLocation })
  })

  it('Connect button disabled when token is empty', () => {
    render(<Login />)
    const btn = screen.getByText('Connect with Token')
    expect(btn.closest('button')).toBeDisabled()
  })

  it('Shows error message on failed login', async () => {
    render(<Login />)
    const input = screen.getByPlaceholderText('Paste your token from: kubectl create token diverge-dashboard')
    fireEvent.change(input, { target: { value: 'invalid-token' } })
    const btn = screen.getByText('Connect with Token')
    fireEvent.click(btn)

    await waitFor(() => {
      expect(screen.getByText('Invalid token. Make sure the token has not expired.')).toBeInTheDocument()
    })
  })
})
