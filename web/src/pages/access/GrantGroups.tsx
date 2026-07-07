import type { Agent, Upstream } from '../../lib/types'
import type { Grant } from '../../lib/grants'
import { AgentCard } from './AgentCard'
import { UpstreamGroupCard } from './UpstreamGroupCard'

/** GrantGroups renders the Access page's granted-rights list in one of two transposed groupings:
 *  by agent (a collapsible AgentCard per agent, including agents with zero grants — the Access page
 *  is the only place to manage agent metadata since the Agents page was removed; upstreams nest
 *  inside), or by upstream (an UpstreamGroupCard per upstream, with agents nested inside — the
 *  "who can reach this upstream" view). Falls back to an empty-state when there is nothing to show:
 *  no agents at all in by-agent mode, or no grants at all in by-upstream mode (grant-centric). */
export function GrantGroups({
  grants, agents, upstreams, by, onChanged,
}: {
  grants: Grant[]
  agents: Agent[]
  upstreams: Upstream[]
  by: 'agent' | 'upstream'
  onChanged: () => void
}) {
  if (by === 'agent') {
    if (agents.length === 0) {
      return (
        <div className="rounded-lg border border-border bg-card px-3 py-6 text-center text-xs text-muted-foreground">
          Прав ещё не выдано — действует запрет по умолчанию
        </div>
      )
    }
    return (
      <div className="space-y-2">
        {agents.map((a) => (
          <AgentCard
            key={a.id}
            agent={a}
            grants={grants.filter((g) => g.agentId === a.id)}
            upstreams={upstreams}
            onChanged={onChanged}
          />
        ))}
      </div>
    )
  }
  if (grants.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-card px-3 py-6 text-center text-xs text-muted-foreground">
        Прав ещё не выдано — действует запрет по умолчанию
      </div>
    )
  }
  // by upstream: the upstream is the container (one UpstreamGroupCard each), with its agents nested
  // inside — the transpose of by-agent mode.
  const byUp = new Map<string, Grant[]>()
  for (const g of grants) byUp.set(g.upstreamId, [...(byUp.get(g.upstreamId) ?? []), g])
  return (
    <div className="space-y-2">
      {[...byUp.entries()].map(([upId, gs]) => (
        <UpstreamGroupCard
          key={upId}
          upstreamId={upId}
          upstream={upstreams.find((u) => u.id === upId)}
          grants={gs}
          agents={agents}
          onChanged={onChanged}
        />
      ))}
    </div>
  )
}
