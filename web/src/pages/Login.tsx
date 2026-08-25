import { useState, useEffect } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { useAuth } from '@/hooks/useAuth'
import { LogIn, Shield } from 'lucide-react'

interface AuthConfig {
  oidcEnabled: boolean
  providerName: string
  loginUrl: string
}

export default function Login() {
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [authConfig, setAuthConfig] = useState<AuthConfig | null>(null)
  const { login } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const from = (location.state as { from?: { pathname: string } })?.from?.pathname ?? '/'

  // Fetch auth config to determine SSO availability
  useEffect(() => {
    fetch('/auth/config')
      .then((res) => {
        if (res.ok) return res.json()
        return null
      })
      .then((data) => {
        if (data) setAuthConfig(data as AuthConfig)
      })
      .catch(() => {
        // OIDC not configured — SSO unavailable
      })
  }, [])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    const ok = await login(token.trim())
    setLoading(false)
    if (ok) navigate(from, { replace: true })
    else setError('Invalid token. Make sure the token has not expired.')
  }

  const handleSSOLogin = () => {
    const returnUrl = encodeURIComponent(from)
    window.location.href = `${authConfig?.loginUrl ?? '/auth/login'}?return_url=${returnUrl}`
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-background p-4">
      <Card className="w-full max-w-md">
        <CardHeader className="text-center">
          <div className="text-4xl mb-2">🔀</div>
          <CardTitle>Diverge</CardTitle>
          <CardDescription>Sign in to your preview environment dashboard</CardDescription>
        </CardHeader>
        <form onSubmit={handleSubmit}>
          <CardContent className="space-y-4">
            {/* SSO Login Button */}
            {authConfig?.oidcEnabled ? (
              <Button
                type="button"
                variant="default"
                className="w-full"
                onClick={handleSSOLogin}
              >
                <Shield className="h-4 w-4 mr-2" />
                Sign in with {authConfig.providerName}
              </Button>
            ) : (
              <Button type="button" variant="outline" className="w-full" disabled title="Not configured">
                <Shield className="h-4 w-4 mr-2" />
                SSO Login (Not Configured)
              </Button>
            )}

            {/* Divider */}
            <div className="relative">
              <div className="absolute inset-0 flex items-center">
                <span className="w-full border-t" />
              </div>
              <div className="relative flex justify-center text-xs uppercase">
                <span className="bg-card px-2 text-muted-foreground">or use a token</span>
              </div>
            </div>

            {/* Token Login */}
            <div>
              <label className="text-sm font-medium mb-1 block">Service Account Token</label>
              <Textarea
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder="Paste your token from: kubectl create token diverge-dashboard"
                rows={4}
                className="font-mono text-xs"
              />
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
          </CardContent>
          <CardFooter className="flex flex-col gap-2">
            <Button type="submit" className="w-full" disabled={loading || !token.trim()}>
              <LogIn className="h-4 w-4 mr-2" />
              {loading ? 'Connecting...' : 'Connect with Token'}
            </Button>
          </CardFooter>
        </form>
      </Card>
    </div>
  )
}
