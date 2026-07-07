import { Globe, Database, Boxes } from 'lucide-react'
import { revokeGrant, ApiError } from '../../lib/api'
import type { Grant } from '../../lib/grants'
import type { Upstream } from '../../lib/types'
import { RuleRow } from './RuleRow'
import { useToastStore } from '../../lib/toast'

/** UpstreamGrantCard renders one grant (an upstream sub-card) in the Access page's granted-rights
 *  section: a one-line header (kind icon, hostname, kind label, Revoke button) followed by the
 *  grant's rules as RuleRows. Revoke removes every rule for the (agent, upstream) pair. */
export function UpstreamGrantCard({
  grant,
  upstream,
  onChanged,
}: {
  grant: Grant
  upstream?: Upstream
  onChanged: () => void
}) {
  const push = useToastStore((s) => s.push)
  const kind = upstream?.kind || upstream?.profile || 'http'
  const iconKind = upstream?.profile ? 'citeck' : upstream?.kind
  const Icon = iconKind === 'k8s' ? Boxes : iconKind === 'citeck' ? Database : Globe
  const host = upstream?.name ?? grant.upstreamId

  async function revoke() {
    try {
      await revokeGrant(grant.agentId, grant.upstreamId)
      push('success', 'Access revoked')
      onChanged()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to revoke')
    }
  }

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-muted/30">
      <div className="flex items-center gap-2 border-b border-border px-3 py-1.5">
        <Icon size={15} className="text-muted-foreground" />
        <span className="font-mono text-[13px]">{host}</span>
        <span className="text-[11px] text-muted-foreground">· {kind}</span>
        <button
          onClick={revoke}
          className="ml-auto rounded border border-border px-2.5 py-1 text-[11px] font-medium text-muted-foreground hover:border-destructive/60 hover:text-destructive"
        >
          Revoke
        </button>
      </div>
      <div className="space-y-1.5 p-2.5">
        {grant.rules.map((r) => (
          <RuleRow key={r.id} rule={r} onChanged={onChanged} />
        ))}
      </div>
    </div>
  )
}
