import { useEffect, useState } from 'react'
import { cn } from '@/lib/utils'

export function TTLCountdown({ expiresAt, className }: { expiresAt?: string; className?: string }) {
  const [now, setNow] = useState(Date.now())

  useEffect(() => {
    if (!expiresAt) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [expiresAt])

  if (!expiresAt) return <span className={cn('text-sm text-muted-foreground', className)}>No TTL</span>

  const expiry = new Date(expiresAt).getTime()
  const diff = expiry - now
  if (diff <= 0) return <span className={cn('text-sm text-red-400', className)}>Expired</span>

  const hours = Math.floor(diff / 3600000)
  const mins = Math.floor((diff % 3600000) / 60000)
  const secs = Math.floor((diff % 60000) / 1000)

  const color = diff > 3600000 ? 'text-green-400' : diff > 600000 ? 'text-yellow-400' : 'text-red-400'
  const display = hours > 0 ? `${hours}h ${mins}m` : mins > 0 ? `${mins}m ${secs}s` : `${secs}s`

  return <span className={cn('text-sm font-mono', color, className)}>{display}</span>
}
