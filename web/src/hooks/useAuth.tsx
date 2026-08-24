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
  const [isLoading, setIsLoading] = useState(!!getToken())

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

  useEffect(() => {
    const t = getToken()
    if (!t) { setIsLoading(false); return }
    validate(t).then((u) => {
      if (u) { setTokenState(t); setUser(u) }
      else { setTokenState(null); setUser(null) }
      setIsLoading(false)
    })
  }, [validate])

  const login = useCallback(async (t: string): Promise<boolean> => {
    setIsLoading(true)
    const u = await validate(t)
    if (u) { setTokenState(t); setUser(u); setIsLoading(false); return true }
    setTokenState(null); setUser(null); setIsLoading(false); return false
  }, [validate])

  const logout = useCallback(() => {
    clearToken(); setTokenState(null); setUser(null)
  }, [])

  return (
    <AuthContext.Provider value={{ token, user, isAuthenticated: !!token && !!user, isLoading, login, logout }}>
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
