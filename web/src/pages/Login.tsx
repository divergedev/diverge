import { useState } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { useAuth } from '@/hooks/useAuth'

export default function Login() {
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const { login } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const from = (location.state as { from?: { pathname: string } })?.from?.pathname ?? '/'

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    const ok = await login(token.trim())
    setLoading(false)
    if (ok) navigate(from, { replace: true })
    else setError('Invalid token. Make sure the token has not expired.')
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
              {loading ? 'Connecting...' : 'Connect'}
            </Button>
            <Button type="button" variant="outline" className="w-full" disabled title="Coming in Phase 2">
              SSO Login (Coming Soon)
            </Button>
          </CardFooter>
        </form>
      </Card>
    </div>
  )
}
