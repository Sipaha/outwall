import { NavLink } from 'react-router'
import { LayoutDashboard, Server, KeyRound, ScrollText, Settings, Lock } from 'lucide-react'
import { useEventStore } from '../lib/events'
import { useOperatorSession } from '../lib/operatorSession'

interface NavItem {
  to: string
  label: string
  icon: typeof LayoutDashboard
}

const NAV: NavItem[] = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/upstreams', label: 'Upstreams', icon: Server },
  { to: '/access', label: 'Access', icon: KeyRound },
  { to: '/audit', label: 'Audit', icon: ScrollText },
  { to: '/settings', label: 'Settings', icon: Settings },
]

export function Sidebar() {
  const connected = useEventStore((s) => s.connected)
  const sessionOpen = useOperatorSession((s) => s.open)
  const lockNow = useOperatorSession((s) => s.lockNow)
  return (
    <aside className="flex h-screen w-52 shrink-0 flex-col border-r border-border bg-card">
      <div className="flex items-center gap-2 px-4 py-3.5 border-b border-border">
        <span className="font-mono text-sm font-semibold tracking-tight text-foreground">outwall</span>
        <span
          className="ml-auto inline-block h-2 w-2 rounded-full"
          title={connected ? 'Live (SSE connected)' : 'Disconnected'}
          style={{
            backgroundColor: connected ? 'var(--color-status-running)' : 'var(--color-status-stopped)',
          }}
        />
      </div>
      <nav className="flex-1 overflow-y-auto px-2 py-2">
        {NAV.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            className={({ isActive }) =>
              `flex items-center gap-2.5 rounded px-2.5 py-1.5 text-[13px] mb-0.5 ${
                isActive
                  ? 'bg-primary/15 text-primary'
                  : 'text-muted-foreground hover:bg-muted hover:text-foreground'
              }`
            }
          >
            <Icon size={15} />
            {label}
          </NavLink>
        ))}
      </nav>
      {sessionOpen && (
        <button
          type="button"
          onClick={() => lockNow()}
          title="Close the operator session (privileged actions will re-prompt for the master password)"
          className="mx-2 mb-2 flex items-center gap-2 rounded px-2.5 py-1.5 text-[13px] text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          <Lock size={13} />
          Lock now
        </button>
      )}
    </aside>
  )
}
