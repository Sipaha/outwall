interface Tab { id: string; label: string }

export function Tabs({ tabs, active, onChange }: { tabs: Tab[]; active: string; onChange: (id: string) => void }) {
  return (
    <div role="tablist" className="flex gap-1 border-b border-border">
      {tabs.map((t) => (
        <button
          key={t.id}
          role="tab"
          aria-selected={active === t.id}
          onClick={() => onChange(t.id)}
          className={
            'px-3 py-1.5 text-sm ' +
            (active === t.id ? 'border-b-2 border-primary text-foreground' : 'text-muted-foreground')
          }
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}
