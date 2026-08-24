import type { ReactNode } from 'react'

export function TopologyColumn({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-3 min-w-[200px]">
      <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wider px-1">
        {title}
      </h3>
      <div className="flex flex-col gap-3">
        {children}
      </div>
    </div>
  )
}
