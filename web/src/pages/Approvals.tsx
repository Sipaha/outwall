import { useCallback, useEffect, useState } from 'react'
import {
  listApprovals,
  resolveApproval,
  listAccessRequests,
  resolveAccessRequest,
  ApiError,
} from '../lib/api'
import type { Approval, AccessRequest } from '../lib/types'
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

export function Approvals() {
  const [approvals, setApprovals] = useState<Approval[]>([])
  const [requests, setRequests] = useState<AccessRequest[]>([])
  const push = useToastStore((s) => s.push)

  const counters = useEventStore((s) => s.counters)
  const refreshKey =
    (counters['approval.enqueued'] ?? 0) +
    (counters['approval.resolved'] ?? 0) +
    (counters['access.requested'] ?? 0)

  const load = useCallback(() => {
    Promise.all([listApprovals(), listAccessRequests()])
      .then(([a, r]) => {
        setApprovals(a ?? [])
        setRequests(r ?? [])
      })
      .catch((err) => {
        push('error', err instanceof ApiError ? err.message : 'Failed to load approvals')
      })
  }, [push])

  useEffect(load, [load, refreshKey])

  async function decide(id: string, approve: boolean) {
    try {
      await resolveApproval(id, approve)
      push('success', approve ? 'Request approved' : 'Request denied')
      load()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to resolve')
    }
  }

  async function resolveAccess(id: string, status: string) {
    try {
      await resolveAccessRequest(id, status)
      push('success', `Access request ${status}`)
      load()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to resolve')
    }
  }

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-lg font-semibold">Approvals</h1>

      <section className="rounded-lg border border-border bg-card">
        <header className="border-b border-border px-3 py-2 text-xs font-semibold text-muted-foreground">
          Pending approvals
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
            { header: 'Purpose', cell: (p) => p.purpose },
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

      <section className="rounded-lg border border-border bg-card">
        <header className="border-b border-border px-3 py-2 text-xs font-semibold text-muted-foreground">
          Access requests
        </header>
        <p className="px-3 py-2 text-[11px] text-muted-foreground">
          Granting marks the request handled — actual access is via Rules.
        </p>
        <DataTable
          rows={requests}
          rowKey={(r) => r.id}
          empty="No access requests"
          columns={[
            { header: 'Agent', cell: (r) => r.agent_name || shortId(r.agent_id) },
            { header: 'Upstream', cell: (r) => r.upstream_name || shortId(r.upstream_id) },
            { header: 'Purpose', cell: (r) => r.purpose },
            { header: 'Status', cell: (r) => <StatusBadge status={r.status} /> },
            { header: 'When', cell: (r) => fmtTime(r.created_at), className: 'text-muted-foreground' },
            {
              header: '',
              cell: (r) =>
                r.status === 'pending' ? (
                  <div className="flex justify-end gap-1.5">
                    <button
                      onClick={() => resolveAccess(r.id, 'granted')}
                      className="rounded bg-success/15 px-2 py-0.5 text-[11px] font-medium text-success hover:bg-success/25"
                    >
                      Grant
                    </button>
                    <button
                      onClick={() => resolveAccess(r.id, 'denied')}
                      className="rounded bg-destructive/15 px-2 py-0.5 text-[11px] font-medium text-destructive hover:bg-destructive/25"
                    >
                      Deny
                    </button>
                    <button
                      onClick={() => resolveAccess(r.id, 'dismissed')}
                      className="rounded bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground hover:bg-muted/70"
                    >
                      Dismiss
                    </button>
                  </div>
                ) : null,
            },
          ]}
        />
      </section>
    </div>
  )
}
