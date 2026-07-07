import { useState } from 'react'
import { Globe, Database, Boxes, ChevronRight } from 'lucide-react'
import { revokeGrant, ApiError } from '../../lib/api'
import type { Agent, Upstream } from '../../lib/types'
import { grantExpiry } from '../../lib/grants'
import type { Grant } from '../../lib/grants'
import { RuleRow } from './RuleRow'
import { useToastStore } from '../../lib/toast'

/**
 * UpstreamGroupCard renders one upstream as the collapsible container in the by-upstream grouping:
 * the upstream header on top (chevron, hostname, kind, agent count) whose click toggles collapse,
 * then a nested section per agent that holds a grant on it (agent label + Revoke + that agent's
 * rules). This mirrors the by-agent AgentCard's collapse behaviour — it is the transpose of the
 * AgentCard → UpstreamGrantCard nesting, so the two toggle modes read differently (here the upstream
 * is the outer object).
 */
export function UpstreamGroupCard({
  upstreamId,
  upstream,
  grants,
  agents,
  onChanged,
}: {
  upstreamId: string
  upstream?: Upstream
  grants: Grant[] // grants against THIS upstream (one per agent)
  agents: Agent[]
  onChanged: () => void
}) {
  const [open, setOpen] = useState(true)
  const push = useToastStore((s) => s.push)
  const iconKind = upstream?.profile ? 'citeck' : upstream?.kind
  const host = upstream?.name ?? upstreamId
  const kind = upstream?.kind || upstream?.profile || 'http'

  async function revoke(g: Grant) {
    try {
      await revokeGrant(g.agentId, g.upstreamId)
      push('success', 'Access revoked')
      onChanged()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to revoke')
    }
  }

  return (
    <div className="overflow-hidden rounded-xl border border-border bg-card">
      <div
        onClick={() => setOpen((o) => !o)}
        className="flex cursor-pointer select-none items-center gap-2.5 border-b border-border bg-muted/30 px-3.5 py-2.5"
      >
        <ChevronRight size={14} className={`text-muted-foreground ${open ? 'rotate-90 transition' : 'transition'}`} />
        <span className="flex h-6 w-6 items-center justify-center rounded bg-primary/15 text-primary">
          {iconKind === 'k8s' ? (
            <Boxes size={14} />
          ) : iconKind === 'citeck' ? (
            <Database size={14} />
          ) : (
            <Globe size={14} />
          )}
        </span>
        <span className="font-mono text-sm">{host}</span>
        <span className="text-[11px] text-muted-foreground">· {kind}</span>
        <span className="ml-auto text-xs text-muted-foreground">
          <b className="font-semibold text-foreground">{grants.length}</b>{' '}
          {grants.length === 1 ? 'агент' : 'агентов'}
        </span>
      </div>
      {open && (
      <div className="space-y-2.5 px-3.5 py-3">
        {grants.map((g) => {
          const agent = agents.find((a) => a.id === g.agentId)
          const name = agent?.name ?? g.agentId.slice(0, 8)
          const exp = grantExpiry(g.rules)
          return (
            <div key={g.agentId} className="overflow-hidden rounded-lg border border-border bg-muted/20">
              <div className="flex items-center gap-2 border-b border-border px-3 py-1.5">
                <span className="flex h-5 w-5 items-center justify-center rounded bg-primary/15 text-[11px] font-bold text-primary">
                  {name.charAt(0).toUpperCase()}
                </span>
                <span className="text-[13px] font-medium">{name}</span>
                <span className="font-mono text-[11px] text-muted-foreground">{g.agentId.slice(0, 8)}</span>
                {exp === 'expired' && <span className="rounded bg-destructive/15 px-1.5 text-[11px] text-destructive">истекло</span>}
                {exp === 'expiring' && <span className="rounded bg-warning/15 px-1.5 text-[11px] text-warning">истекает</span>}
                <button
                  onClick={() => revoke(g)}
                  className="ml-auto rounded border border-border px-2.5 py-1 text-[11px] font-medium text-muted-foreground hover:border-destructive/60 hover:text-destructive"
                >
                  Revoke
                </button>
              </div>
              <div className="space-y-1.5 p-2.5">
                {g.rules.map((r) => (
                  <RuleRow key={r.id} rule={r} onChanged={onChanged} />
                ))}
              </div>
            </div>
          )
        })}
      </div>
      )}
    </div>
  )
}
