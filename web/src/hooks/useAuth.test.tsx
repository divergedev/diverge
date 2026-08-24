import { render, screen, waitFor, renderHook, act } from '../test/utils'
import { AuthProvider, useAuth, ProtectedRoute } from './useAuth'
import { MemoryRouter, Route, Routes } from 'react-router-dom'

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
    expect(result.current.user).toBeTruthy()
    expect(localStorage.getItem('diverge:token')).toBe('valid-token')
  })

  it('Login returns false on invalid token', async () => {
    // We would need to mock the API to fail. For now, assuming validate fails if we give it 'invalid-token'
    // But our MSW handler always succeeds in the simple mock. Let's make the MSW handler fail for 'invalid-token' if needed.
    // Assuming we don't change MSW for this basic test unless needed.
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
    expect(localStorage.getItem('diverge:token')).toBeNull()
  })
})

describe('ProtectedRoute', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('Redirects to /login when unauthenticated', async () => {
    render(
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

    render(
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
