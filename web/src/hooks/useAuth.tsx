import { createContext, useContext, useState, useEffect, useCallback, type ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { authClient, getToken, setToken as storeToken, clearToken } from '@/api/client'
import type { GetCurrentUserResponse } from '@/api/gen/diverge/v1alpha1/auth_pb'

interface AuthContextValue {
  token: string | null
  user: GetCurrentUserResponse | null
  isAuthenticated: boolean
  isLoading: boolean
  login: (token: string) => Promise<boolean>
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setTokenState] = useState<string | null>(getToken)
  const [user, setUser] = useState<GetCurrentUserResponse | null>(null)
  const [isLoading, setIsLoading] = useState(true) // Always check on mount

  const validate = useCallback(async (t: string): Promise<GetCurrentUserResponse | null> => {
    try {
      storeToken(t)
      const resp = await authClient.getCurrentUser({})
      return resp
    } catch {
      clearToken()
      return null
    }
  }, [])

  // Try cookie-based auth first (OIDC), then localStorage token
  useEffect(() => {
    const tryAuth = async () => {
      // First try cookie-based auth (no token needed — cookie sent automatically)
      try {
        const resp = await authClient.getCurrentUser({})
        if (resp) {
          setUser(resp)
          setIsLoading(false)
          return
        }
      } catch {
        // Cookie auth failed — try localStorage token
      }

      // Fall back to localStorage token
      const t = getToken()
      if (!t) { setIsLoading(false); return }
      const u = await validate(t)
      if (u) { setTokenState(t); setUser(u) }
      else { setTokenState(null); setUser(null) }
      setIsLoading(false)
    }
    tryAuth()
  }, [validate])

  const login = useCallback(async (t: string): Promise<boolean> => {
    setIsLoading(true)
    const u = await validate(t)
    if (u) { setTokenState(t); setUser(u); setIsLoading(false); return true }
    setTokenState(null); setUser(null); setIsLoading(false); return false
  }, [validate])

  const logout = useCallback(async () => {
    // Clear server-side session cookie
    try {
      await fetch('/auth/logout', { method: 'POST', credentials: 'same-origin' })
    } catch {
      // Best effort — clear local state regardless
    }
    clearToken()
    setTokenState(null)
    setUser(null)
  }, [])

  const isAuthenticated = !!user

  return (
    <AuthContext.Provider value={{ token, user, isAuthenticated, isLoading, login, logout }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}

export function ProtectedRoute({ children }: { children: ReactNode }) {
  const { isAuthenticated, isLoading } = useAuth()
  const location = useLocation()

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-background">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary" />
      </div>
    )
  }

  if (!isAuthenticated) {
    return <Navigate to="/login" state={{ from: location }} replace />
  }

  return <>{children}</>
}
