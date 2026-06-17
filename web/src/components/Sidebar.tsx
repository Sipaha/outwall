import { NavLink } from 'react-router'
import { LayoutDashboard, Server, Bot, ShieldCheck, CheckSquare, ScrollText, Settings } from 'lucide-react'
import { useEventStore } from '../lib/events'

interface NavItem {
  to: string
  label: string
  icon: typeof LayoutDashboard
}

// Only Dashboard is wired in 6A; the rest are routes Plan 6B fills.
const NAV: NavItem[] = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/upstreams', label: 'Upstreams', icon: Server },
  { to: '/agents', label: 'Agents', icon: Bot },
  { to: '/rules', label: 'Rules', icon: ShieldCheck },
  { to: '/approvals', label: 'Approvals', icon: CheckSquare },
  { to: '/audit', label: 'Audit', icon: ScrollText },
  { to: '/settings', label: 'Settings', icon: Settings },
]

export function Sidebar() {
  const connected = useEventStore((s) => s.connected)
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
    </aside>
  )
}
