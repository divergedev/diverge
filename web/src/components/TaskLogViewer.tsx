import { useEffect, useRef, useState, useCallback } from 'react'
import { agentTaskClient } from '@/api/client'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { AlertCircle } from 'lucide-react'

interface TaskLogEntry {
  timestamp: string
  message: string
}

export function TaskLogViewer({ namespace, name, className }: { namespace: string; name: string; className?: string }) {
  const [lines, setLines] = useState<TaskLogEntry[]>([])
  const [follow, setFollow] = useState(true)
  const [showTimestamps, setShowTimestamps] = useState(true)
  const [streamError, setStreamError] = useState<string | null>(null)
  const [retryCount, setRetryCount] = useState(0)
  const containerRef = useRef<HTMLDivElement>(null)
  const userScrolledRef = useRef(false)

  useEffect(() => {
    setLines([])
    setStreamError(null)
    userScrolledRef.current = false
    if (containerRef.current) {
      containerRef.current.scrollTop = 0
    }

    const ac = new AbortController()
    const decoder = new TextDecoder()

    async function stream() {
      let remainder = ''
      let lastTimestamp = ''
      try {
        for await (const msg of agentTaskClient.streamTaskLogs(
          { namespace, name, follow: true, tailLines: BigInt(200) },
          { signal: ac.signal },
        )) {
          const text = decoder.decode(msg.chunk, { stream: true })
          if (!text) continue
          const ts = msg.timestamp ? msg.timestamp.toDate().toISOString() : lastTimestamp
          lastTimestamp = ts

          const combined = remainder + text
          const parts = combined.split(/\r?\n/)
          remainder = parts.pop() ?? ''

          const completedLines = parts
            .filter((line) => line.length > 0)
            .map((line) => ({ timestamp: ts, message: line }))
          if (completedLines.length === 0) continue

          setLines((prev) => {
            const next = [...prev, ...completedLines]
            return next.length > 10000 ? next.slice(-10000) : next
          })
        }

        const flushed = remainder + decoder.decode()
        if (flushed.length > 0) {
          const finalLines = flushed
            .split(/\r?\n/)
            .filter((line) => line.length > 0)
            .map((line) => ({ timestamp: lastTimestamp, message: line }))
          if (finalLines.length > 0) {
            setLines((prev) => {
              const next = [...prev, ...finalLines]
              return next.length > 10000 ? next.slice(-10000) : next
            })
          }
        }
      } catch (err) {
        if (!ac.signal.aborted) {
          console.error('[task-log-viewer] stream error:', err)
          setStreamError(err instanceof Error ? err.message : 'Failed to stream sandbox logs')
        }
      }
    }

    stream()
    return () => ac.abort()
  }, [namespace, name, retryCount])

  useEffect(() => {
    if (follow && containerRef.current && !userScrolledRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight
    }
  }, [lines, follow])

  const handleScroll = useCallback(() => {
    const el = containerRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 50
    userScrolledRef.current = !atBottom
    if (atBottom) setFollow(true)
  }, [])

  return (
    <div className={cn('flex flex-col rounded-md border bg-black', className)}>
      <div className="flex items-center gap-2 p-2 border-b border-border/50">
        <Button size="sm" variant={follow ? 'default' : 'outline'} onClick={() => { setFollow(!follow); userScrolledRef.current = false }}>
          Follow
        </Button>
        <Button size="sm" variant={showTimestamps ? 'default' : 'outline'} onClick={() => setShowTimestamps(!showTimestamps)}>
          Timestamps
        </Button>
        <Button size="sm" variant="outline" onClick={() => setLines([])}>
          Clear
        </Button>
        <span className="ml-auto text-xs text-muted-foreground">{lines.length} lines</span>
      </div>
      {streamError && (
        <div className="bg-destructive/15 border-b border-destructive/30 text-destructive px-3 py-2 flex items-center justify-between text-xs" role="alert">
          <span className="flex items-center gap-2">
            <AlertCircle className="h-4 w-4 flex-shrink-0" />
            {streamError}
          </span>
          <Button size="sm" variant="outline" onClick={() => setRetryCount((c) => c + 1)}>
            Retry
          </Button>
        </div>
      )}
      <div ref={containerRef} onScroll={handleScroll} className="overflow-auto p-3 font-mono text-xs leading-5 h-[500px]">
        {lines.length === 0 ? (
          <div className="text-muted-foreground text-center py-8">
            {streamError ? 'Log stream disconnected.' : 'Waiting for sandbox logs...'}
          </div>
        ) : (
          lines.map((line, i) => (
            <div key={i} className="hover:bg-white/5">
              {showTimestamps && line.timestamp && <span className="text-muted-foreground mr-2">{line.timestamp}</span>}
              <span className="text-gray-200">{line.message}</span>
            </div>
          ))
        )}
      </div>
    </div>
  )
}
