import { useEffect, useRef } from 'react'
import { X } from 'lucide-react'

interface ModalProps {
  open: boolean
  title: string
  onClose: () => void
  /** sm = 360px, md = 480px (default), lg = 640px. All capped at 90vw. */
  width?: 'sm' | 'md' | 'lg'
  children: React.ReactNode
  footer?: React.ReactNode
  /** When set, the body + footer are wrapped in a <form> so Enter submits. */
  onSubmit?: (e: React.FormEvent) => void
}

/** Centered native <dialog> with the dark theme tokens, a header (title + close X), a
 *  scrollable body, and an optional footer row. Mirrors the launcher's Modal idiom. */
export function Modal({ open, title, onClose, width = 'md', children, footer, onSubmit }: ModalProps) {
  const ref = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    const dlg = ref.current
    if (!dlg) return
    if (open && !dlg.open) dlg.showModal()
    else if (!open && dlg.open) dlg.close()
  }, [open])

  const widthClass = width === 'sm' ? 'w-[360px]' : width === 'lg' ? 'w-[640px]' : 'w-[480px]'

  const body = (
    <>
      <div className="flex items-center justify-between border-b border-border px-4 py-2.5">
        <h2 className="text-sm font-semibold">{title}</h2>
        <button
          type="button"
          className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
          onClick={onClose}
          aria-label="Close"
        >
          <X size={14} />
        </button>
      </div>
      <div className="space-y-3 px-4 py-3 max-h-[70vh] overflow-y-auto">{children}</div>
      {footer && (
        <div className="flex items-center justify-between border-t border-border px-4 py-2.5">{footer}</div>
      )}
    </>
  )

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      className={`fixed inset-0 z-50 m-auto ${widthClass} max-w-[90vw] rounded-lg border border-border bg-card p-0 text-foreground shadow-xl`}
    >
      {onSubmit ? (
        <form onSubmit={onSubmit} className="flex flex-col">
          {body}
        </form>
      ) : (
        <div className="flex flex-col">{body}</div>
      )}
    </dialog>
  )
}
