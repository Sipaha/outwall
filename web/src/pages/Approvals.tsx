import { useCallback, useEffect, useState } from 'react'
import {
  listApprovals,
  resolveApproval,
  listAccessRequests,
  resolveAccessRequest,
  ApiError,
} from '../lib/api'
import type { Approval, AccessRequest, ResolveOptions, UpstreamAuthConfig } from '../lib/types'
import { useEventStore } from '../lib/events'
import { DataTable } from '../components/DataTable'
import { Modal } from '../components/Modal'
import { StatusBadge } from '../components/StatusBadge'
import { FormField, fieldControlClass } from '../components/FormField'
import { Select } from '../components/Select'
import { useToastStore } from '../lib/toast'

function shortId(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id
}

function fmtTime(iso: string): string {
  const d = new Date(iso)
  return isNaN(d.getTime()) ? iso : d.toLocaleString()
}

/**
 * exampleURL builds the concrete URL the operator is approving by substituting the agent's
 * requested values into the operation path/query template. Variable placeholders that have no
 * requested value keep their `{name:type}` form so the operator still sees the shape.
 */
function exampleURL(a: Approval): string {
  const values = a.op_values ?? {}
  const subst = (s: string) =>
    s.replace(/\{(\w+):\w+\}/g, (m, name: string) => (name in values ? values[name] : m))
  let url = `https://${a.host ?? ''}${subst(a.op_path_template ?? '')}`
  const query = a.op_query_template ?? {}
  const qs = Object.entries(query)
    .map(([k, v]) => `${k}=${subst(v)}`)
    .join('&')
  if (qs) url += `?${qs}`
  return `${a.op_method ?? ''} ${url}`.trim()
}

// segmentsOf splits a path template into fixed vs `{name:type}` variable pieces so the card can
// render variable segments visually distinct from the fixed structure.
function segmentsOf(template: string): { text: string; variable: boolean }[] {
  const out: { text: string; variable: boolean }[] = []
  const re = /\{(\w+):(\w+)\}/g
  let last = 0
  let m: RegExpExecArray | null
  while ((m = re.exec(template)) !== null) {
    if (m.index > last) out.push({ text: template.slice(last, m.index), variable: false })
    out.push({ text: m[0], variable: true })
    last = m.index + m[0].length
  }
  if (last < template.length) out.push({ text: template.slice(last), variable: false })
  return out
}

const cardClass = 'rounded-lg border border-border bg-card p-3 space-y-3'
const approveBtn =
  'rounded bg-success/15 px-2.5 py-1 text-[11px] font-medium text-success hover:bg-success/25'
const denyBtn =
  'rounded bg-destructive/15 px-2.5 py-1 text-[11px] font-medium text-destructive hover:bg-destructive/25'
const trustBtn =
  'rounded bg-primary/15 px-2.5 py-1 text-[11px] font-medium text-primary hover:bg-primary/25'

interface CardProps {
  approval: Approval
  onResolve: (id: string, approve: boolean, opts?: ResolveOptions) => void
}

/** Tier-1 host card: agent + host + purpose, with an optional credential to attach on approve. */
function HostCard({ approval, onResolve }: CardProps) {
  const [auth, setAuth] = useState<UpstreamAuthConfig>({ type: 'static' })

  function approve() {
    // Attach the credential only when the operator chose an auth type (None = attach later).
    onResolve(approval.id, true, auth.type === 'none' ? undefined : { auth })
  }

  return (
    <div className={cardClass}>
      <div className="flex items-start justify-between gap-2">
        <div className="space-y-1">
          <div className="text-[11px] text-muted-foreground">
            Host access · agent <span className="font-mono">{shortId(approval.agent_id)}</span>
          </div>
          <div className="font-mono text-sm">{approval.host}</div>
          {approval.purpose && <div className="text-xs text-muted-foreground">{approval.purpose}</div>}
        </div>
        <StatusBadge status="host" />
      </div>

      <div className="rounded border border-border/60 bg-muted/30 p-2 space-y-2">
        <div className="text-[11px] text-muted-foreground">Credential (attach now or later)</div>
        <FormField label="Auth type">
          <Select
            value={auth.type}
            onChange={(t) => setAuth({ type: t })}
            options={[
              { value: 'static', label: 'Static header / API key' },
              { value: 'basic', label: 'Basic' },
              { value: 'none', label: 'None (attach later)' },
            ]}
          />
        </FormField>
        {auth.type === 'static' && (
          <>
            <FormField label="Header">
              <input
                className={fieldControlClass}
                value={auth.header ?? ''}
                onChange={(e) => setAuth({ ...auth, header: e.target.value })}
                placeholder="Authorization"
                aria-label="Header"
              />
            </FormField>
            <FormField label="Value">
              <input
                className={fieldControlClass}
                value={auth.token ?? ''}
                onChange={(e) => setAuth({ ...auth, token: e.target.value })}
                placeholder="Bearer …"
                aria-label="Value"
              />
            </FormField>
          </>
        )}
        {auth.type === 'basic' && (
          <>
            <FormField label="Username">
              <input
                className={fieldControlClass}
                value={auth.username ?? ''}
                onChange={(e) => setAuth({ ...auth, username: e.target.value })}
                aria-label="Username"
              />
            </FormField>
            <FormField label="Password">
              <input
                className={fieldControlClass}
                type="password"
                value={auth.password ?? ''}
                onChange={(e) => setAuth({ ...auth, password: e.target.value })}
                aria-label="Password"
              />
            </FormField>
          </>
        )}
      </div>

      <div className="flex justify-end gap-1.5">
        <button onClick={approve} className={approveBtn}>
          Approve
        </button>
        <button onClick={() => onResolve(approval.id, false)} className={denyBtn}>
          Deny
        </button>
      </div>
    </div>
  )
}

/** Tier-2 operation card: the operation form, a concrete example URL, per-text-var trust-any. */
function OperationCard({ approval, onResolve }: CardProps) {
  const vars = approval.op_variables ?? []
  const textVars = vars.filter((v) => v.type === 'text')
  const [trust, setTrust] = useState<Record<string, boolean>>({})
  const anyTrusted = Object.values(trust).some(Boolean)

  function approve() {
    const trust_any = textVars.filter((v) => trust[v.name]).map((v) => v.name)
    onResolve(approval.id, true, trust_any.length > 0 ? { trust_any } : { trust_any: [] })
  }

  return (
    <div className={cardClass}>
      <div className="flex items-start justify-between gap-2">
        <div className="space-y-1">
          <div className="text-[11px] text-muted-foreground">
            Operation access · agent <span className="font-mono">{shortId(approval.agent_id)}</span> ·{' '}
            <span className="font-mono">{approval.host}</span>
          </div>
          {approval.purpose && <div className="text-xs text-muted-foreground">{approval.purpose}</div>}
        </div>
        <StatusBadge status="operation" />
      </div>

      {/* The operation shape: fixed vs variable segments visually distinct. */}
      <div className="font-mono text-xs">
        <span className="font-semibold">{approval.op_method} </span>
        {segmentsOf(approval.op_path_template ?? '').map((s, i) => (
          <span
            key={i}
            className={s.variable ? 'rounded bg-primary/15 px-1 text-primary' : 'text-foreground'}
          >
            {s.text}
          </span>
        ))}
        {Object.entries(approval.op_query_template ?? {}).map(([k, v], i) => (
          <span key={k}>
            {i === 0 ? '?' : '&'}
            {k}=
            {segmentsOf(v).map((s, j) => (
              <span
                key={j}
                className={s.variable ? 'rounded bg-primary/15 px-1 text-primary' : 'text-foreground'}
              >
                {s.text}
              </span>
            ))}
          </span>
        ))}
      </div>

      {/* The concrete example URL built from the agent's requested values. */}
      <div className="rounded border border-border/60 bg-muted/30 p-2">
        <div className="mb-1 text-[11px] text-muted-foreground">Example request</div>
        <code className="block break-all text-xs">{exampleURL(approval)}</code>
      </div>

      {/* Per-text-variable controls: the requested value + a trust-any toggle. */}
      {vars.length > 0 && (
        <div className="space-y-1.5">
          {vars.map((v) => (
            <div key={v.name} className="flex items-center justify-between gap-2 text-xs">
              <div className="font-mono">
                {v.name}
                <span className="ml-1 text-muted-foreground">:{v.type}</span>
                {v.name in (approval.op_values ?? {}) && (
                  <span className="ml-2 text-muted-foreground">= {approval.op_values![v.name]}</span>
                )}
              </div>
              {v.type === 'text' ? (
                <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                  <input
                    type="checkbox"
                    checked={!!trust[v.name]}
                    onChange={(e) => setTrust({ ...trust, [v.name]: e.target.checked })}
                    aria-label={`Trust any value for ${v.name}`}
                  />
                  trust any value
                </label>
              ) : (
                <span className="text-[11px] text-muted-foreground">auto (date)</span>
              )}
            </div>
          ))}
        </div>
      )}

      {anyTrusted && (
        <div className="rounded border border-warning/40 bg-warning/10 px-2 py-1.5 text-[11px] text-warning">
          ⚠ Trust-any grants access to ANY value for the checked variable(s) — review carefully
          before approving.
        </div>
      )}

      <div className="flex justify-end gap-1.5">
        <button onClick={approve} className={approveBtn}>
          Approve
        </button>
        <button onClick={() => onResolve(approval.id, false)} className={denyBtn}>
          Deny
        </button>
      </div>
    </div>
  )
}

/** MCP k8s-access card: cluster + (namespace / resource) + verb + purpose. No credential form —
 *  k8s clusters are already credentialed; approve creates an agent-scoped allow rule. */
function K8sAccessCard({ approval, onResolve }: CardProps) {
  const p = approval
  return (
    <div className={cardClass}>
      <div className="flex items-start justify-between gap-2">
        <div className="space-y-1">
          <div className="text-[11px] text-muted-foreground">
            k8s access · agent <span className="font-mono">{shortId(p.agent_id)}</span> ·{' '}
            <span className="font-mono">{p.host}</span>
          </div>
          <div className="font-mono text-xs">
            <span className="text-muted-foreground">{p.namespace || '*'}</span>
            <span className="text-muted-foreground">{' / '}</span>
            <span>{p.resource || '*'}</span> <StatusBadge status={p.verb || '*'} />
          </div>
          {p.purpose && <div className="text-xs text-muted-foreground">{p.purpose}</div>}
        </div>
        <StatusBadge status="k8s" />
      </div>
      <div className="flex justify-end gap-1.5">
        <button onClick={() => onResolve(p.id, true)} className={approveBtn}>
          Approve
        </button>
        <button onClick={() => onResolve(p.id, false)} className={denyBtn}>
          Deny
        </button>
      </div>
    </div>
  )
}

/** Data-plane new-value card: the template + the new (variable, value); approve / approve+trust. */
function NewValueCard({ approval, onResolve }: CardProps) {
  const newVals = approval.new_values ?? []

  function approve(trustAny: boolean) {
    const opts: ResolveOptions = trustAny
      ? { trust_any: newVals.map((nv) => nv.var) }
      : { trust_any: [] }
    onResolve(approval.id, true, opts)
  }

  return (
    <div className={cardClass}>
      <div className="flex items-start justify-between gap-2">
        <div className="space-y-1">
          <div className="text-[11px] text-muted-foreground">
            New value · agent <span className="font-mono">{shortId(approval.agent_id)}</span>
          </div>
          <div className="font-mono text-xs">
            <span className="font-semibold text-muted-foreground">{approval.method || '*'} </span>
            <span>{approval.template}</span>
          </div>
        </div>
        <StatusBadge status="new-value" />
      </div>

      <div className="space-y-1">
        {newVals.map((nv) => (
          <div key={nv.var} className="flex items-center gap-2 text-xs">
            <span className="font-mono text-muted-foreground">{nv.var}</span>
            <span className="text-muted-foreground">=</span>
            <span className="rounded bg-primary/15 px-1 font-mono text-primary">{nv.value}</span>
          </div>
        ))}
      </div>

      <div className="flex justify-end gap-1.5">
        <button onClick={() => approve(false)} className={approveBtn}>
          Approve
        </button>
        <button onClick={() => approve(true)} className={trustBtn}>
          Approve + trust any
        </button>
        <button onClick={() => onResolve(approval.id, false)} className={denyBtn}>
          Deny
        </button>
      </div>
    </div>
  )
}

/** Plain http / k8s approval (empty Kind, no new_values): the legacy method+path / tuple card. */
function PlainCard({ approval, onResolve }: CardProps) {
  const p = approval
  const isK8s = !!(p.namespace || p.resource || p.verb)
  return (
    <div className={cardClass}>
      <div className="flex items-start justify-between gap-2">
        <div className="space-y-1">
          <div className="text-[11px] text-muted-foreground">
            agent <span className="font-mono">{shortId(p.agent_id)}</span> · upstream{' '}
            <span className="font-mono">{shortId(p.upstream_id)}</span>
          </div>
          {isK8s ? (
            <div className="font-mono text-xs">
              <span className="text-muted-foreground">{p.namespace || '*'}</span>
              <span className="text-muted-foreground">{' / '}</span>
              <span>{p.resource || '*'}</span> <StatusBadge status={p.verb || '*'} />
            </div>
          ) : (
            <div className="font-mono text-xs">{(p.method || '*') + ' ' + p.path}</div>
          )}
          {p.purpose && <div className="text-xs text-muted-foreground">{p.purpose}</div>}
          {p.request_body && (
            <pre className="max-h-40 max-w-md overflow-auto rounded border border-border bg-muted/40 p-2 text-[11px] whitespace-pre-wrap">
              {p.request_body}
            </pre>
          )}
        </div>
      </div>
      <div className="flex justify-end gap-1.5">
        <button onClick={() => onResolve(p.id, true)} className={approveBtn}>
          Approve
        </button>
        <button onClick={() => onResolve(p.id, false)} className={denyBtn}>
          Deny
        </button>
      </div>
    </div>
  )
}

function ApprovalCard(props: CardProps) {
  const { approval } = props
  if (approval.kind === 'host-access') return <HostCard {...props} />
  if (approval.kind === 'operation') return <OperationCard {...props} />
  if (approval.kind === 'k8s-access') return <K8sAccessCard {...props} />
  if ((approval.new_values?.length ?? 0) > 0) return <NewValueCard {...props} />
  return <PlainCard {...props} />
}

export function Approvals() {
  const [approvals, setApprovals] = useState<Approval[]>([])
  const [requests, setRequests] = useState<AccessRequest[]>([])
  // Deny-with-reason: clicking Deny opens this modal; the (optional) reason is sent to the agent.
  const [denyId, setDenyId] = useState<string | null>(null)
  const [denyReason, setDenyReason] = useState('')
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
              header: '',
              cell: (r) =>
                r.status === 'pending' ? (
                  <div className="flex justify-end">
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
    </div>
  )
}
