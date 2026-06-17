import { useCallback, useEffect, useState } from 'react'
import { listAgents, listApprovals, resolveApproval, ApiError } from '../lib/api'
import type { Agent, Approval } from '../lib/types'
import { useEventStore } from '../lib/events'
import { DataTable } from '../components/DataTable'
import { StatusBadge } from '../components/StatusBadge'
import { useToastStore } from '../lib/toast'

function shortId(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id
}

function fmtTime(iso: string): string {
  const d = new Date(iso)
  return isNaN(d.getTime()) ? iso : d.toLocaleString()
}

export function Dashboard() {
  const [agents, setAgents] = useState<Agent[]>([])
  const [approvals, setApprovals] = useState<Approval[]>([])
  const push = useToastStore((s) => s.push)

  // Re-fetch whenever a relevant SSE event arrives (the store bumps a per-type counter).
  const counters = useEventStore((s) => s.counters)
  const refreshKey =
    (counters['agent.registered'] ?? 0) +
    (counters['approval.enqueued'] ?? 0) +
    (counters['approval.resolved'] ?? 0)

  const load = useCallback(() => {
    // setState lives in the .then/.catch callbacks (deferred past the fetch) — the form the
    // react-hooks rule endorses for "subscribe to an external system" effects.
    Promise.all([listAgents(), listApprovals()])
      .then(([a, p]) => {
        setAgents(a ?? [])
        setApprovals(p ?? [])
      })
      .catch((err) => {
        push('error', err instanceof ApiError ? err.message : 'Failed to load dashboard')
      })
  }, [push])

  // Re-fetch on mount and whenever an SSE event bumps refreshKey.
  useEffect(load, [load, refreshKey])

  async function decide(id: string, approve: boolean) {
    try {
      await resolveApproval(id, approve)
      push('success', approve ? 'Request approved' : 'Request denied')
      // SSE approval.resolved will trigger a refresh; refresh eagerly too.
      load()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to resolve')
    }
  }

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-lg font-semibold">Dashboard</h1>

      <section className="rounded-lg border border-border bg-card">
        <header className="border-b border-border px-3 py-2 text-xs font-semibold text-muted-foreground">
          Agents
        </header>
        <DataTable
          rows={agents}
          rowKey={(a) => a.id}
          empty="No agents yet"
          columns={[
            { header: 'Name', cell: (a) => a.name },
            { header: 'Status', cell: (a) => <StatusBadge status={a.status} /> },
            { header: 'ID', cell: (a) => shortId(a.id), className: 'font-mono text-muted-foreground' },
          ]}
        />
      </section>

      <section className="rounded-lg border border-border bg-card">
        <header className="border-b border-border px-3 py-2 text-xs font-semibold text-muted-foreground">
          Approval queue
        </header>
        <DataTable
          rows={approvals}
          rowKey={(p) => p.id}
          empty="No pending approvals"
          columns={[
            { header: 'Agent', cell: (p) => shortId(p.agent_id), className: 'font-mono' },
            { header: 'Upstream', cell: (p) => shortId(p.upstream_id), className: 'font-mono' },
            { header: 'Method', cell: (p) => p.method || '*', className: 'font-mono' },
            { header: 'Path', cell: (p) => p.path, className: 'font-mono' },
            { header: 'When', cell: (p) => fmtTime(p.created_at), className: 'text-muted-foreground' },
            {
              header: '',
              cell: (p) => (
                <div className="flex justify-end gap-1.5">
                  <button
                    onClick={() => decide(p.id, true)}
                    className="rounded bg-success/15 px-2 py-0.5 text-[11px] font-medium text-success hover:bg-success/25"
                  >
                    Approve
                  </button>
                  <button
                    onClick={() => decide(p.id, false)}
                    className="rounded bg-destructive/15 px-2 py-0.5 text-[11px] font-medium text-destructive hover:bg-destructive/25"
                  >
                    Deny
                  </button>
                </div>
              ),
            },
          ]}
        />
      </section>
    </div>
  )
}
