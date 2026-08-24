import { render as rtlRender, screen, waitFor } from '@testing-library/react'
import { renderHook, act } from '../test/utils'
import { AuthProvider, useAuth, ProtectedRoute } from './useAuth'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ThemeProvider } from '@/components/ThemeProvider'

// For ProtectedRoute tests that need their own MemoryRouter with initialEntries,
// we use raw RTL render with manual providers (no MemoryRouter from test/utils)
function renderWithProviders(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return rtlRender(
    <QueryClientProvider client={qc}>
      <ThemeProvider defaultTheme="dark">
        <AuthProvider>
          {ui}
        </AuthProvider>
      </ThemeProvider>
    </QueryClientProvider>
  )
}

describe('useAuth and AuthProvider', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('Login stores token and validates via API', async () => {
    const { result } = renderHook(() => useAuth())

    let success = false
    await act(async () => {
      success = await result.current.login('valid-token')
    })

    expect(success).toBe(true)
    expect(result.current.isAuthenticated).toBe(true)
  })

  it('Logout clears token and user', async () => {
    const { result } = renderHook(() => useAuth())

    await act(async () => {
      await result.current.login('valid-token')
    })

    act(() => {
      result.current.logout()
    })

    expect(result.current.isAuthenticated).toBe(false)
    expect(result.current.user).toBeNull()
  })

  it('Shows loading spinner while validating', async () => {
    localStorage.setItem('diverge:token', 'valid-token')
    const { result } = renderHook(() => useAuth())

    // Initially loading
    expect(result.current.isLoading).toBe(true)

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false)
    })
  })
})

describe('ProtectedRoute', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('Redirects to /login when unauthenticated', async () => {
    renderWithProviders(
      <MemoryRouter initialEntries={['/protected']}>
        <Routes>
          <Route path="/login" element={<div>Login Page</div>} />
          <Route path="/protected" element={
            <ProtectedRoute>
              <div>Protected Content</div>
            </ProtectedRoute>
          } />
        </Routes>
      </MemoryRouter>
    )

    expect(await screen.findByText('Login Page')).toBeInTheDocument()
    expect(screen.queryByText('Protected Content')).not.toBeInTheDocument()
  })

  it('Renders children when authenticated', async () => {
    localStorage.setItem('diverge:token', 'valid-token')

    renderWithProviders(
      <MemoryRouter initialEntries={['/protected']}>
        <Routes>
          <Route path="/protected" element={
            <ProtectedRoute>
              <div>Protected Content</div>
            </ProtectedRoute>
          } />
        </Routes>
      </MemoryRouter>
    )

    expect(await screen.findByText('Protected Content')).toBeInTheDocument()
  })
})

describe('useAuth Negative Cases', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('Login returns false on invalid token', async () => {
    const { result } = renderHook(() => useAuth())

    let success = true
    await act(async () => {
      success = await result.current.login('invalid-token')
    })

    expect(success).toBe(false)
    expect(result.current.isAuthenticated).toBe(false)
    expect(result.current.user).toBeNull()
  })
})
