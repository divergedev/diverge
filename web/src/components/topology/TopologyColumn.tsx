import type { ReactNode } from 'react'

export function TopologyColumn({
  title,
  badge,
  children,
}: {
  title: string
  badge?: ReactNode
  children: ReactNode
}) {
  return (
    <div className="flex flex-col gap-3 min-w-[200px]">
      <div className="flex items-center justify-between px-1">
        <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
          {title}
        </h3>
        {badge}
      </div>
      <div className="flex flex-col gap-3">
        {children}
      </div>
    </div>
  )
}
