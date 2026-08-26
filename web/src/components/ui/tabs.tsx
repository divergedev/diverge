import { createContext, useContext, useState, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

interface TabsContextValue { value: string; onChange: (v: string) => void }
const TabsContext = createContext<TabsContextValue>({ value: '', onChange: () => {} })

export function Tabs({ defaultValue, value: controlledValue, onValueChange, children, className }: { defaultValue?: string; value?: string; onValueChange?: (v: string) => void; children: ReactNode; className?: string }) {
  const [internalValue, setInternalValue] = useState(defaultValue ?? controlledValue ?? '')
  const value = controlledValue ?? internalValue
  const onChange = onValueChange ?? setInternalValue
  return <TabsContext.Provider value={{ value, onChange }}><div className={className}>{children}</div></TabsContext.Provider>
}

export function TabsList({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('inline-flex h-10 items-center justify-center rounded-md bg-muted p-1 text-muted-foreground', className)}>{children}</div>
}

export function TabsTrigger({ value, children, className }: { value: string; children: ReactNode; className?: string }) {
  const ctx = useContext(TabsContext)
  return (
    <button
      className={cn('inline-flex items-center justify-center whitespace-nowrap rounded-sm px-3 py-1.5 text-sm font-medium ring-offset-background transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
        ctx.value === value && 'bg-background text-foreground shadow-sm', className)}
      onClick={() => ctx.onChange(value)}
    >{children}</button>
  )
}

export function TabsContent({ value, children, className }: { value: string; children: ReactNode; className?: string }) {
  const ctx = useContext(TabsContext)
  if (ctx.value !== value) return null
  return <div className={cn('mt-2 ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2', className)}>{children}</div>
}
