import { useCallback, useEffect, useState } from 'react'
import { Plus } from 'lucide-react'
import {
  listRules, listAgents, listUpstreams, listApprovals, listAccessRequests, ApiError,
} from '../lib/api'
import type { Agent, Upstream, Rule, Approval, AccessRequest } from '../lib/types'
import { deriveGrants } from '../lib/grants'
import { useAccessGrouping } from '../lib/accessGrouping'
import { useEventStore } from '../lib/events'
import { useToastStore } from '../lib/toast'
import { RequestsPanel } from './access/RequestsPanel'
import { GrantGroups } from './access/GrantGroups'
import { ManualRuleModal } from './access/ManualRuleModal'

/** Access is the page that unifies rights management: the pending-approvals queue (RequestsPanel)
 *  on top, then the granted-rights list (GrantGroups) — grouped by agent or by upstream — derived
 *  from rules + the resolved access-request history, plus a manual-grant entry point
 *  (ManualRuleModal) for creating a rule directly without going through a request. */
export function Access() {
  const [rules, setRules] = useState<Rule[]>([])
  const [agents, setAgents] = useState<Agent[]>([])
  const [upstreams, setUpstreams] = useState<Upstream[]>([])
  const [approvals, setApprovals] = useState<Approval[]>([])
  const [requests, setRequests] = useState<AccessRequest[]>([])
  const [manualOpen, setManualOpen] = useState(false)
  const by = useAccessGrouping((s) => s.by)
  const setBy = useAccessGrouping((s) => s.setBy)
  const push = useToastStore((s) => s.push)

  const counters = useEventStore((s) => s.counters)
  const refreshKey =
    (counters['rule.created'] ?? 0) + (counters['rule.updated'] ?? 0) +
    (counters['approval.enqueued'] ?? 0) + (counters['approval.resolved'] ?? 0) +
    (counters['access.requested'] ?? 0) + (counters['access.revoked'] ?? 0) +
    (counters['agent.registered'] ?? 0)

  const load = useCallback(() => {
    Promise.all([listRules(), listAgents(), listUpstreams(), listApprovals(), listAccessRequests()])
      .then(([r, a, u, ap, req]) => {
        setRules(r ?? []); setAgents(a ?? []); setUpstreams(u ?? [])
        setApprovals(ap ?? []); setRequests(req ?? [])
      })
      .catch((err) => push('error', err instanceof ApiError ? err.message : 'Failed to load access'))
  }, [push])

  useEffect(load, [load, refreshKey])

  const grants = deriveGrants(rules, requests)

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-lg font-semibold">Access</h1>

      <RequestsPanel approvals={approvals} onChanged={load} />

      <div className="flex items-center gap-3">
        <span className="text-[13px] font-semibold">Выданные права</span>
        <div className="inline-flex overflow-hidden rounded border border-border bg-card">
          <button onClick={() => setBy('agent')}
            className={`px-3 py-1 text-xs ${by === 'agent' ? 'bg-primary/15 text-primary' : 'text-muted-foreground'}`}>
            По агенту
          </button>
          <button onClick={() => setBy('upstream')}
            className={`px-3 py-1 text-xs ${by === 'upstream' ? 'bg-primary/15 text-primary' : 'text-muted-foreground'}`}>
            По upstream
          </button>
        </div>
        <button onClick={() => setManualOpen(true)}
          className="ml-auto inline-flex items-center gap-1.5 rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:opacity-90">
          <Plus size={13} /> Выдать вручную
        </button>
      </div>

      <GrantGroups grants={grants} agents={agents} upstreams={upstreams} by={by} onChanged={load} />

      <ManualRuleModal open={manualOpen} onClose={() => setManualOpen(false)} onCreated={() => { setManualOpen(false); load() }} />
    </div>
  )
}
