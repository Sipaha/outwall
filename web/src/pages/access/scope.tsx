interface ScopeBadgeProps {
  scope: { label: string; kind: 'method' | 'read' | 'write' | 'verb' | 'browse' }
}

// Colour maps to the app's semantic tokens: write is the loudest (warning), reads/methods calmer.
const CLASS: Record<ScopeBadgeProps['scope']['kind'], string> = {
  method: 'bg-success/15 text-success',
  read: 'bg-primary/15 text-primary',
  write: 'bg-warning/15 text-warning',
  verb: 'bg-primary/15 text-primary',
  browse: 'bg-muted text-muted-foreground',
}

export function ScopeBadge({ scope }: ScopeBadgeProps) {
  return (
    <span className={`rounded px-2 py-0.5 text-[11px] font-bold tracking-wide ${CLASS[scope.kind]}`}>
      {scope.label}
    </span>
  )
}
