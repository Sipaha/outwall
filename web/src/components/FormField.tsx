import type { ReactNode } from 'react'

/** Shared themed className for native form controls (input/select). Border --color-border,
 *  bg --color-muted, focus ring --color-primary — matches the dark-console look. */
export const fieldControlClass =
  'w-full rounded border border-border bg-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary'

interface FormFieldProps {
  label: string
  hint?: ReactNode
  children: ReactNode
}

/** A labelled control wrapper: a small uppercase-ish label above its control, optional hint
 *  below. Used by the Upstreams / Rules / Settings forms. */
export function FormField({ label, hint, children }: FormFieldProps) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-muted-foreground">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-[11px] text-muted-foreground">{hint}</span>}
    </label>
  )
}
