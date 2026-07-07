import { useCallback, useEffect, useState } from 'react'
import {
  listApprovals,
  resolveApproval,
  listAccessRequests,
  resolveAccessRequest,
  revokeAccessRequest,
  ApiError,
} from '../lib/api'
import type { Approval, AccessRequest, ResolveOptions } from '../lib/types'
import { useEventStore } from '../lib/events'
import { DataTable } from '../components/DataTable'
import { Modal } from '../components/Modal'
import { StatusBadge } from '../components/StatusBadge'
import { FormField, fieldControlClass } from '../components/FormField'
import { useToastStore } from '../lib/toast'
import { ApprovalCard, shortId } from './access/ApprovalCards'

function fmtTime(iso: string): string {
  const d = new Date(iso)
  return isNaN(d.getTime()) ? iso : d.toLocaleString()
}

export function Approvals() {
  const [approvals, setApprovals] = useState<Approval[]>([])
  const [requests, setRequests] = useState<AccessRequest[]>([])
  // Deny-with-reason: clicking Deny opens this modal; the (optional) reason is sent to the agent.
  const [denyId, setDenyId] = useState<string | null>(null)
  const [denyReason, setDenyReason] = useState('')
  // Revoke-with-confirm: clicking Revoke on a granted access request opens this modal.
  const [revokeId, setRevokeId] = useState<string | null>(null)
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

  async function decide(id: string, approve: boolean, opts?: ResolveOptions) {
    try {
      // Pass opts only when present so a plain approve stays a 2-arg call (no { trust_any: undefined }).
      await (opts ? resolveApproval(id, approve, opts) : resolveApproval(id, approve))
      push('success', approve ? 'Request approved' : 'Request denied')
      load()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to resolve')
    }
  }

  // openDeny is passed to cards in place of an immediate deny: it opens the reason modal.
  function openDeny(id: string) {
    setDenyId(id)
    setDenyReason('')
  }

  function confirmDeny(e?: React.FormEvent) {
    e?.preventDefault()
    const id = denyId
    setDenyId(null)
    if (id) void decide(id, false, denyReason.trim() ? { reason: denyReason.trim() } : undefined)
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

  async function confirmRevoke() {
    const id = revokeId
    setRevokeId(null)
    if (!id) return
    try {
      await revokeAccessRequest(id)
      push('success', 'Access revoked')
      load()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to revoke')
    }
  }

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-lg font-semibold">Approvals</h1>

      <section className="space-y-2">
        <header className="text-xs font-semibold text-muted-foreground">Pending approvals</header>
        {approvals.length === 0 ? (
          <div className="rounded-lg border border-border bg-card px-3 py-6 text-center text-xs text-muted-foreground">
            No pending approvals
          </div>
        ) : (
          <div className="space-y-2">
            {approvals.map((a) => (
              <ApprovalCard
                key={a.id}
                approval={a}
                onResolve={(id, approve, opts) => {
                  // Deny routes through the reason modal; approve goes straight through.
                  if (!approve) openDeny(id)
                  else void decide(id, true, opts)
                }}
              />
            ))}
          </div>
        )}
      </section>

      <section className="rounded-lg border border-border bg-card">
        <header className="border-b border-border px-3 py-2 text-xs font-semibold text-muted-foreground">
          Access requests
        </header>
        <p className="px-3 py-2 text-[11px] text-muted-foreground">
          History of agent requests — act on them in the cards above. Dismiss clears a stale row.
        </p>
        <DataTable
          rows={requests}
          rowKey={(r) => r.id}
          empty="No access requests"
          columns={[
            { header: 'Agent', cell: (r) => r.agent_name || shortId(r.agent_id) },
            { header: 'Upstream', cell: (r) => r.upstream_name || shortId(r.upstream_id) },
            { header: 'Purpose', cell: (r) => r.purpose },
            {
              header: 'Status',
              cell: (r) => (
                <div className="space-y-0.5">
                  <StatusBadge status={r.status} />
                  {r.status === 'denied' && r.reason && (
                    <div className="text-[11px] text-muted-foreground italic">{r.reason}</div>
                  )}
                </div>
              ),
            },
            { header: 'When', cell: (r) => fmtTime(r.created_at), className: 'text-muted-foreground' },
            {
              header: 'Granted',
              cell: (r) => (r.status === 'granted' ? fmtTime(r.resolved_at) : '—'),
              className: 'text-muted-foreground',
            },
            {
              header: '',
              cell: (r) => {
                if (r.status === 'pending') {
                  return (
                    <div className="flex justify-end">
                      <button
                        onClick={() => resolveAccess(r.id, 'dismissed')}
                        className="rounded bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground hover:bg-muted/70"
                      >
                        Dismiss
                      </button>
                    </div>
                  )
                }
                if (r.status === 'granted') {
                  return (
                    <div className="flex justify-end">
                      <button
                        onClick={() => setRevokeId(r.id)}
                        className="rounded bg-destructive/15 px-2 py-0.5 text-[11px] font-medium text-destructive hover:bg-destructive/25"
                      >
                        Revoke
                      </button>
                    </div>
                  )
                }
                return null
              },
            },
          ]}
        />
      </section>

      <Modal
        open={denyId !== null}
        title="Deny request"
        onClose={() => setDenyId(null)}
        onSubmit={confirmDeny}
        footer={
          <>
            <button
              type="button"
              onClick={() => setDenyId(null)}
              className="rounded bg-muted px-3 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground"
            >
              Cancel
            </button>
            <button type="submit" className="rounded bg-destructive px-3 py-1.5 text-xs font-medium text-white hover:opacity-90">
              Deny
            </button>
          </>
        }
      >
        <FormField label="Reason (optional — shown to the agent)">
          <textarea
            className={fieldControlClass}
            rows={3}
            value={denyReason}
            onChange={(e) => setDenyReason(e.target.value)}
            placeholder="e.g. not allowed on production"
            aria-label="Deny reason"
            autoFocus
          />
        </FormField>
      </Modal>

      <Modal
        open={revokeId !== null}
        title="Revoke access"
        onClose={() => setRevokeId(null)}
        width="sm"
        footer={
          <>
            <button
              type="button"
              onClick={() => setRevokeId(null)}
              className="rounded bg-muted px-3 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={confirmRevoke}
              className="rounded bg-destructive px-3 py-1.5 text-xs font-medium text-white hover:opacity-90"
            >
              Revoke
            </button>
          </>
        }
      >
        <p className="text-xs text-muted-foreground">
          Revoke this grant? The agent's rules for this upstream will be removed.
        </p>
      </Modal>
    </div>
  )
}
