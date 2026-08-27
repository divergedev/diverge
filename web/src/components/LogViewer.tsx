import { useEffect, useRef, useState, useCallback } from 'react'
import { environmentClient } from '@/api/client'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface LogLine { pod: string; timestamp: string; message: string }

export function LogViewer({ namespace, environmentName, hookType, className }: { namespace: string; environmentName: string; hookType?: string; className?: string }) {
  const [lines, setLines] = useState<LogLine[]>([])
  const [follow, setFollow] = useState(true)
  const [showTimestamps, setShowTimestamps] = useState(true)
  const containerRef = useRef<HTMLDivElement>(null)
  const userScrolledRef = useRef(false)

  useEffect(() => {
    const ac = new AbortController()
    async function stream() {
      try {
        for await (const msg of environmentClient.streamLogs(
          { namespace, environmentName, hookType: hookType ?? '', follow: !hookType, tailLines: BigInt(hookType ? 100 : 200) },
          { signal: ac.signal },
        )) {
          setLines((prev) => {
            const ts = msg.timestamp ? msg.timestamp.toDate().toISOString() : ''
            const next = [...prev, { pod: msg.podName, timestamp: ts, message: msg.content }]
            return next.length > 10000 ? next.slice(-10000) : next
          })
        }
      } catch (err) {
        if (!ac.signal.aborted) console.error('[log-viewer] stream error:', err)
      }
    }
    stream()
    return () => ac.abort()
  }, [namespace, environmentName, hookType])

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
      <div ref={containerRef} onScroll={handleScroll} className="overflow-auto p-3 font-mono text-xs leading-5 h-[500px]">
        {lines.length === 0 ? (
          <div className="text-muted-foreground text-center py-8">Waiting for logs...</div>
        ) : (
          lines.map((line, i) => (
            <div key={i} className="hover:bg-white/5">
              {showTimestamps && <span className="text-muted-foreground mr-2">{line.timestamp}</span>}
              <span className="text-blue-400 mr-2">{line.pod}</span>
              <span className="text-gray-200">{line.message}</span>
            </div>
          ))
        )}
      </div>
    </div>
  )
}
