import { createContext, useContext, useEffect, useId, useRef, useCallback, useState, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

interface DialogTitleContextValue {
  titleId: string
  setCustomTitleId: (id: string | undefined) => void
}

const DialogTitleIdContext = createContext<DialogTitleContextValue | undefined>(undefined)

function isPointerOutsideDialog(e: { clientX: number; clientY: number }, dialog: HTMLDialogElement): boolean {
  const r = dialog.getBoundingClientRect()
  if (r.width === 0 && r.height === 0) {
    return false
  }
  return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom
}

export function Dialog({ open, onClose, children }: { open: boolean; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null)
  const pressedOutsideRef = useRef(false)
  const defaultTitleId = useId()
  const [customTitleId, setCustomTitleId] = useState<string | undefined>(undefined)
  const effectiveTitleId = customTitleId ?? defaultTitleId

  useEffect(() => {
    const el = ref.current
    if (!el) return
    if (open && !el.open) el.showModal()
    else if (!open && el.open) el.close()
  }, [open])

  const handlePointerDown = useCallback((e: React.PointerEvent<HTMLDialogElement>) => {
    const el = ref.current
    if (!el) return
    pressedOutsideRef.current = isPointerOutsideDialog(e, el) || e.target === el
  }, [])

  const handlePointerUp = useCallback((e: React.PointerEvent<HTMLDialogElement>) => {
    const el = ref.current
    if (!el) return
    const outside = isPointerOutsideDialog(e, el) || e.target === el
    if (pressedOutsideRef.current && outside) {
      pressedOutsideRef.current = false
      onClose()
    } else {
      pressedOutsideRef.current = false
    }
  }, [onClose])

  const handleClose = useCallback(() => {
    if (ref.current) {
      ref.current.returnValue = ''
    }
    if (open) {
      onClose()
    }
  }, [onClose, open])

  return (
    <DialogTitleIdContext.Provider value={{ titleId: defaultTitleId, setCustomTitleId }}>
      <dialog
        ref={ref}
        aria-labelledby={effectiveTitleId}
        className="backdrop:bg-black/50 bg-transparent p-0 m-auto max-w-[calc(100vw-2rem)]"
        onPointerDown={handlePointerDown}
        onPointerUp={handlePointerUp}
        onClose={handleClose}
      >
        {open && children}
      </dialog>
    </DialogTitleIdContext.Provider>
  )
}

export function DialogContent({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('bg-background text-foreground rounded-lg border shadow-lg p-6 w-full max-w-lg', className)}>{children}</div>
}

export function DialogHeader({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('flex flex-col space-y-1.5 mb-4', className)}>{children}</div>
}

export function DialogTitle({ children, className, id }: { children: ReactNode; className?: string; id?: string }) {
  const ctx = useContext(DialogTitleIdContext)
  const setCustomTitleId = ctx?.setCustomTitleId
  useEffect(() => {
    if (!setCustomTitleId) return
    setCustomTitleId(id)
    return () => setCustomTitleId(undefined)
  }, [setCustomTitleId, id])
  return <h1 id={id ?? ctx?.titleId} className={cn('text-lg font-semibold leading-none tracking-tight', className)}>{children}</h1>
}

export function DialogDescription({ children, className }: { children: ReactNode; className?: string }) {
  return <p className={cn('text-sm text-muted-foreground', className)}>{children}</p>
}

export function DialogFooter({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('flex justify-end gap-2 mt-4', className)}>{children}</div>
}
