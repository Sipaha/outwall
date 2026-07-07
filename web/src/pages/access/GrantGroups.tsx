import type { Agent, Upstream } from '../../lib/types'
import type { Grant } from '../../lib/grants'
import { AgentCard } from './AgentCard'
import { UpstreamGrantCard } from './UpstreamGrantCard'

/** GrantGroups renders the Access page's granted-rights list, grouped either by agent (one
 *  collapsible AgentCard per agent that has at least one grant) or by upstream (one header per
 *  upstream, with an agent-labelled UpstreamGrantCard per grant against it). Falls back to an
 *  empty-state message when there are no grants at all. */
export function GrantGroups({
  grants, agents, upstreams, by, onChanged,
}: {
  grants: Grant[]
  agents: Agent[]
  upstreams: Upstream[]
  by: 'agent' | 'upstream'
  onChanged: () => void
}) {
  if (grants.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-card px-3 py-6 text-center text-xs text-muted-foreground">
        Прав ещё не выдано — действует запрет по умолчанию
      </div>
    )
  }
  if (by === 'agent') {
    const withGrants = agents.filter((a) => grants.some((g) => g.agentId === a.id))
    return (
      <div className="space-y-2">
        {withGrants.map((a) => (
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
  // by upstream: one group per upstream, each grant rendered as an UpstreamGrantCard (which already
  // shows the upstream's hostname in its own header) with an "агент X" label above it — no separate
  // upstream-name header here, since that would duplicate the hostname the card already renders.
  const byUp = new Map<string, Grant[]>()
  for (const g of grants) byUp.set(g.upstreamId, [...(byUp.get(g.upstreamId) ?? []), g])
  return (
    <div className="space-y-3">
      {[...byUp.entries()].map(([upId, gs]) => (
        <div key={upId} className="space-y-1.5">
          {gs.map((g) => {
            const agent = agents.find((a) => a.id === g.agentId)
            const up = upstreams.find((u) => u.id === upId)
            return (
              <div key={g.agentId}>
                <div className="mb-1 px-1 text-[11px] text-muted-foreground">агент {agent?.name ?? g.agentId}</div>
                <UpstreamGrantCard grant={g} upstream={up} onChanged={onChanged} />
              </div>
            )
          })}
        </div>
      ))}
    </div>
  )
}
