import { useState } from 'react'
import { useNavigate } from 'react-router'
import { ChevronRight, Trash2, MoreVertical, ArrowRight } from 'lucide-react'
import { deleteAgent, ApiError } from '../../lib/api'
import type { Agent, Upstream } from '../../lib/types'
import type { Grant } from '../../lib/grants'
import { UpstreamGrantCard } from './UpstreamGrantCard'
import { RelTime } from '../../components/RelTime'
import { useToastStore } from '../../lib/toast'

/** AgentCard renders one agent's collapsible group in the Access page's granted-rights section:
 *  a header (chevron, avatar, name, short id, status dot, rule/resource counter, last-active meta,
 *  a trash icon that deletes the agent immediately, and a ⋮ kebab menu) followed by one
 *  UpstreamGrantCard per grant when expanded. Header click toggles collapse; the trash icon and
 *  kebab stop propagation so they don't also toggle it. */
export function AgentCard({
  agent,
  grants,
  upstreams,
  onChanged,
}: {
  agent: Agent
  grants: Grant[]
  upstreams: Upstream[]
  onChanged: () => void
}) {
  const [open, setOpen] = useState(true)
  const [menu, setMenu] = useState(false)
  const navigate = useNavigate()
  const push = useToastStore((s) => s.push)
  const upstreamOf = (id: string) => upstreams.find((u) => u.id === id)
  const ruleCount = grants.reduce((n, g) => n + g.rules.length, 0)

  async function remove() {
    try {
      await deleteAgent(agent.id)
      push('success', `Agent "${agent.name}" deleted`)
      onChanged()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to delete agent')
    }
  }

  return (
    <div className="relative overflow-hidden rounded-xl border border-border bg-card">
      <div
        onClick={() => setOpen((o) => !o)}
        className="flex cursor-pointer select-none items-center gap-2.5 px-3.5 py-2.5"
      >
        <ChevronRight size={14} className={`text-muted-foreground ${open ? 'rotate-90 transition' : 'transition'}`} />
        <span className="flex h-6 w-6 items-center justify-center rounded bg-primary/15 text-[12px] font-bold text-primary">
          {agent.name.charAt(0).toUpperCase()}
        </span>
        <span className="text-sm font-semibold">{agent.name}</span>
        <span className="font-mono text-[11px] text-muted-foreground">{agent.id.slice(0, 8)}</span>
        <span
          className="inline-block h-1.5 w-1.5 rounded-full"
          style={{ backgroundColor: 'var(--color-status-running)' }}
          title={agent.status}
        />
        <span className="text-xs text-muted-foreground">
          <b className="font-semibold text-foreground">{ruleCount}</b> прав ·{' '}
          <b className="font-semibold text-foreground">{grants.length}</b> ресурсов
        </span>
        <div className="ml-auto flex items-center gap-3 text-[11px] text-muted-foreground">
          <span>активн. <b className="font-medium text-foreground"><RelTime iso={agent.last_seen_at} /></b></span>
          <div className="flex items-center gap-1">
            <button
              onClick={(e) => { e.stopPropagation(); void remove() }}
              aria-label={`Delete agent ${agent.name}`}
              className="rounded p-1 text-muted-foreground hover:bg-destructive/15 hover:text-destructive"
            >
              <Trash2 size={13} />
            </button>
            <div className="relative">
              <button
                onClick={(e) => { e.stopPropagation(); setMenu((m) => !m) }}
                aria-label="Agent menu"
                className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
              >
                <MoreVertical size={13} />
              </button>
              {menu && (
                <div
                  onClick={(e) => e.stopPropagation()}
                  className="absolute right-0 top-7 z-10 w-52 rounded-lg border border-border bg-card p-1 shadow-xl"
                >
                  <button
                    onClick={() => { setMenu(false); navigate(`/audit?tab=requests&agent=${agent.id}`) }}
                    className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-xs hover:bg-muted"
                  >
                    <ArrowRight size={13} className="text-muted-foreground" /> История запросов в Audit
                  </button>
                  <button
                    onClick={() => { setMenu(false); void remove() }}
                    className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-xs text-destructive hover:bg-muted"
                  >
                    <Trash2 size={13} /> Удалить агента
                  </button>
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
      {open && (
        <div className="space-y-2.5 border-t border-border px-3.5 py-3">
          {grants.map((g) => (
            <UpstreamGrantCard key={g.upstreamId} grant={g} upstream={upstreamOf(g.upstreamId)} onChanged={onChanged} />
          ))}
        </div>
      )}
    </div>
  )
}
