import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ThemeProvider } from '@/components/ThemeProvider'
import { AuthProvider } from '@/hooks/useAuth'
import Login from './Login'
import { server } from '../test/mocks/server'
import { http, HttpResponse } from 'msw'
import fc from 'fast-check'

const renderLoginWithState = (pathname: string) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MemoryRouter initialEntries={[{ pathname: '/login', state: { from: { pathname } } }]}>
      <QueryClientProvider client={queryClient}>
        <ThemeProvider defaultTheme="dark">
          <AuthProvider>
            <Login />
          </AuthProvider>
        </ThemeProvider>
      </QueryClientProvider>
    </MemoryRouter>
  )
}

describe('Login PBT', () => {
  it('any return URL is properly encoded in SSO redirect', async () => {
    server.use(
      http.get('*/auth/config', () => {
        return HttpResponse.json({ oidcEnabled: true, providerName: 'PBT', loginUrl: '/auth/login' })
      })
    )

    await fc.assert(
      fc.asyncProperty(fc.string(), async (path) => {
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

        renderLoginWithState(path)

        const btn = await screen.findByText('Sign in with PBT')
        fireEvent.click(btn)

        expect((window.location.href as string).endsWith(`/auth/login?return_url=${encodeURIComponent(path)}`)).toBe(true)
        Object.defineProperty(window, 'location', { writable: true, value: originalLocation })
        cleanup()
      }),
      { numRuns: 20 }
    )
  })
})
