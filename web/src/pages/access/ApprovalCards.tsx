/* eslint-disable react-refresh/only-export-components --
 * This module is a shared card library, not a fast-refresh boundary: it intentionally exports
 * helpers (shortId, exampleURL, segmentsOf) alongside the card components for reuse by both
 * Approvals.tsx and the Access page. */
import { useState, useEffect, type ReactNode } from 'react'
import { MessageSquare } from 'lucide-react'
import { previewPreset } from '../../lib/api'
import type { Approval, ResolveOptions, UpstreamAuthConfig } from '../../lib/types'
import { StatusBadge } from '../../components/StatusBadge'
import { FormField, fieldControlClass } from '../../components/FormField'
import { Select } from '../../components/Select'
import { RelTime } from '../../components/RelTime'
import { ScopeBadge } from './scope'
import { DurationSelect, DEFAULT_TTL_SECONDS } from './DurationSelect'

export function shortId(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id
}

/**
 * exampleURL builds the concrete URL the operator is approving by substituting the agent's
 * requested values into the operation path/query template. Variable placeholders that have no
 * requested value keep their `{name:type}` form so the operator still sees the shape.
 */
export function exampleURL(a: Approval): string {
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
export function segmentsOf(template: string): { text: string; variable: boolean }[] {
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

// Segments renders a path/query template inline, highlighting `{name:type}` variables as chips.
function Segments({ template }: { template: string }) {
  return (
    <>
      {segmentsOf(template).map((s, i) => (
        <span key={i} className={s.variable ? 'rounded bg-primary/15 px-1 text-primary' : 'text-foreground'}>
          {s.text}
        </span>
      ))}
    </>
  )
}

const cardClass = 'rounded-lg border border-border bg-card p-3 space-y-2'
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

/**
 * CardHeader is the "право — герой" header row shared by every restyled card: agent → upstream,
 * a type tag, a right-aligned muted timestamp, and the resolve actions. Agent name and time live
 * here ONLY — the body below never repeats them.
 */
function CardHeader({
  agentId,
  upstream,
  tag,
  createdAt,
  actions,
}: {
  agentId: string
  upstream: string
  tag: string
  createdAt: string
  actions: ReactNode
}) {
  return (
    <div className="flex items-center gap-2">
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5 text-xs">
        <span className="font-semibold">{shortId(agentId)}</span>
        <span className="text-muted-foreground">→</span>
        <span className="truncate font-mono text-muted-foreground">{upstream}</span>
        <StatusBadge status={tag} />
      </div>
      <span className="shrink-0 text-[11px] text-muted-foreground"><RelTime iso={createdAt} empty="" /></span>
      <div className="flex shrink-0 gap-1.5">{actions}</div>
    </div>
  )
}

/** HeroBox is the bordered "what this grants" box: a ScopeBadge + the concrete resource/path. */
function HeroBox({ children }: { children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-md border border-border bg-card px-2.5 py-1.5">
      {children}
    </div>
  )
}

/** PurposeLine is the readable "why" line under the hero box. Omits agent/time (already in header). */
function PurposeLine({ purpose }: { purpose?: string }) {
  if (!purpose) return null
  return (
    <div className="flex items-start gap-1.5 text-[13px] text-foreground">
      <MessageSquare size={13} className="mt-0.5 shrink-0 text-muted-foreground" />
      <span>{purpose}</span>
    </div>
  )
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
      <CardHeader
        agentId={approval.agent_id}
        upstream={approval.host || shortId(approval.upstream_id)}
        tag="host"
        createdAt={approval.created_at}
        actions={
          <>
            <button onClick={approve} className={approveBtn}>
              Approve
            </button>
            <button onClick={() => onResolve(approval.id, false)} className={denyBtn}>
              Deny
            </button>
          </>
        }
      />

      <HeroBox>
        <ScopeBadge scope={{ label: 'HOST', kind: 'browse' }} />
        <span className="font-mono text-[13px] font-semibold">Full access to this host</span>
      </HeroBox>

      <PurposeLine purpose={approval.purpose} />

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
    </div>
  )
}

/** Tier-2 operation card: the operation form, a concrete example URL, per-text-var trust-any. */
function OperationCard({ approval, onResolve }: CardProps) {
  const vars = approval.op_variables ?? []
  const textVars = vars.filter((v) => v.type === 'text')
  const [trust, setTrust] = useState<Record<string, boolean>>({})
  const [ttl, setTtl] = useState(DEFAULT_TTL_SECONDS)
  const anyTrusted = Object.values(trust).some(Boolean)

  function approve() {
    const trust_any = textVars.filter((v) => trust[v.name]).map((v) => v.name)
    onResolve(approval.id, true, trust_any.length > 0 ? { trust_any, ttl_seconds: ttl } : { trust_any: [], ttl_seconds: ttl })
  }

  return (
    <div className={cardClass}>
      <CardHeader
        agentId={approval.agent_id}
        upstream={approval.host || shortId(approval.upstream_id)}
        tag="operation"
        createdAt={approval.created_at}
        actions={
          <>
            <DurationSelect value={ttl} onChange={setTtl} />
            <button onClick={approve} className={approveBtn}>
              Approve
            </button>
            <button onClick={() => onResolve(approval.id, false)} className={denyBtn}>
              Deny
            </button>
          </>
        }
      />

      {/* The hero box: the method scope + the resource shape, variables highlighted as chips. */}
      <HeroBox>
        <ScopeBadge scope={{ label: approval.op_method || 'GET', kind: 'method' }} />
        <span className="font-mono text-[13px] font-semibold">
          <Segments template={approval.op_path_template ?? ''} />
          {Object.entries(approval.op_query_template ?? {}).map(([k, v], i) => (
            <span key={k}>
              {i === 0 ? '?' : '&'}
              {k}=
              <Segments template={v} />
            </span>
          ))}
        </span>
      </HeroBox>

      <PurposeLine purpose={approval.purpose} />

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
    </div>
  )
}

/** MCP k8s-access card: cluster + (namespace / resource) + verb + purpose. No credential form —
 *  k8s clusters are already credentialed; approve creates an agent-scoped allow rule. */
function K8sAccessCard({ approval, onResolve }: CardProps) {
  const p = approval
  // One card can carry several (namespace, resource, verb) tuples; fall back to the single fields.
  const grants =
    p.k8s_grants && p.k8s_grants.length > 0
      ? p.k8s_grants
      : [{ namespace: p.namespace ?? '', resource: p.resource ?? '', verb: p.verb ?? '' }]
  return (
    <div className={cardClass}>
      <CardHeader
        agentId={p.agent_id}
        upstream={p.host || shortId(p.upstream_id)}
        tag="k8s"
        createdAt={p.created_at}
        actions={
          <>
            <button onClick={() => onResolve(p.id, true)} className={approveBtn}>
              Approve
            </button>
            <button onClick={() => onResolve(p.id, false)} className={denyBtn}>
              Deny
            </button>
          </>
        }
      />

      <div className="space-y-1.5">
        {grants.map((g, i) => (
          <HeroBox key={i}>
            <ScopeBadge scope={{ label: g.verb || '*', kind: 'verb' }} />
            <span className="font-mono text-[13px] font-semibold">
              <span className="text-muted-foreground">{g.namespace || '*'}</span>
              <span className="text-muted-foreground"> / </span>
              <span>{g.resource || '*'}</span>
            </span>
          </HeroBox>
        ))}
      </div>

      <PurposeLine purpose={p.purpose} />
    </div>
  )
}

// presetScopeFromPreview derives the scope badge from what the preset ACTUALLY creates (the live
// `previewPreset` rules), never from its name. Safety asymmetry: understating scope (READ on a
// grant that can write) is dangerous, so READ is shown ONLY when EVERY rule is a recognised
// read-only form; any write signal is READ/WRITE; anything unrecognised — or the preview not yet
// loaded — falls back to a neutral badge rather than a possibly-wrong READ.
//
// Preview rule strings come from summarizeTemplate (internal/daemon/admin_preset.go):
//   browse:  "<outcome> browse <methods> <path>"     e.g. "allow browse GET,HEAD /**"
//   profile: "<outcome> <profile> <paramsJSON>"      e.g. `allow citeck {"op":"write",...}`
function presetScopeFromPreview(preview: string[]): {
  label: string
  kind: 'read' | 'write' | 'browse'
} {
  const NEUTRAL = { label: 'СМ. НИЖЕ', kind: 'browse' as const }
  if (preview.length === 0) return NEUTRAL // preview not loaded yet — don't assert a scope

  const isWrite = (r: string) =>
    /"op"\s*:\s*"write"/.test(r) || /\bbrowse\b[^\n]*\b(POST|PUT|PATCH|DELETE)\b/i.test(r)
  if (preview.some(isWrite)) return { label: 'READ/WRITE', kind: 'write' }

  // A rule is provably read-only only in a recognised shape: an allow-browse limited to GET/HEAD,
  // or an allow-profile with "op":"read". Any other shape → neutral (we can't prove read-only).
  const isReadOnly = (r: string) =>
    /^allow\s+browse\s+(?:GET|HEAD)(?:,(?:GET|HEAD))*\s+\S+$/i.test(r) ||
    /^allow\s+\S+\s+.*"op"\s*:\s*"read"/i.test(r)
  if (preview.every(isReadOnly)) return { label: 'READ', kind: 'read' }

  return NEUTRAL
}

/** MCP preset card: editable slots (seeded from the requested bindings) + a live rule preview. */
function PresetCard({ approval, onResolve }: CardProps) {
  const preset = approval.preset
  const [bindings, setBindings] = useState<Record<string, string>>(approval.bindings ?? {})
  const [preview, setPreview] = useState<string[]>([])
  const [ttl, setTtl] = useState(DEFAULT_TTL_SECONDS)

  useEffect(() => {
    let live = true
    previewPreset(approval.upstream_id, approval.preset_id ?? '', bindings)
      .then((r) => {
        if (live) setPreview(r.rules)
      })
      .catch(() => {
        if (live) setPreview([])
      })
    return () => {
      live = false
    }
  }, [approval.upstream_id, approval.preset_id, bindings])

  function setSlot(key: string, value: string) {
    setBindings((b) => ({ ...b, [key]: value }))
  }

  const presetScope = presetScopeFromPreview(preview)

  return (
    <div className={cardClass}>
      <CardHeader
        agentId={approval.agent_id}
        upstream={approval.host || shortId(approval.upstream_id)}
        tag="preset"
        createdAt={approval.created_at}
        actions={
          <>
            <DurationSelect value={ttl} onChange={setTtl} />
            <button
              onClick={() => onResolve(approval.id, true, { bindings, ttl_seconds: ttl })}
              className={approveBtn}
            >
              Approve
            </button>
            <button onClick={() => onResolve(approval.id, false)} className={denyBtn}>
              Deny
            </button>
          </>
        }
      />

      <HeroBox>
        <ScopeBadge scope={presetScope} />
        <span className="font-mono text-[13px] font-semibold">{preset?.label ?? approval.preset_id}</span>
      </HeroBox>

      <PurposeLine purpose={approval.purpose} />

      {(preset?.slots ?? []).length > 0 && (
        <div className="space-y-2">
          {preset!.slots.map((s) => (
            <FormField key={s.key} label={s.label || s.key}>
              {s.type === 'enum' ? (
                <Select
                  value={bindings[s.key] ?? ''}
                  onChange={(v) => setSlot(s.key, v)}
                  options={[
                    ...(s.allow_any ? [{ value: '*', label: '* (all)' }] : []),
                    ...(s.options ?? []).map((o) => ({ value: o, label: o })),
                  ]}
                />
              ) : (
                <input
                  className={fieldControlClass}
                  value={bindings[s.key] ?? ''}
                  onChange={(e) => setSlot(s.key, e.target.value)}
                  aria-label={s.key}
                  placeholder={s.allow_any ? '* or a concrete value' : 'a concrete value'}
                />
              )}
            </FormField>
          ))}
        </div>
      )}

      <div className="rounded border border-border/60 bg-muted/30 p-2">
        <div className="mb-1 text-[11px] text-muted-foreground">Will create</div>
        {preview.length === 0 ? (
          <div className="text-[11px] text-muted-foreground italic">…</div>
        ) : (
          preview.map((r, i) => (
            <code key={i} className="block break-all text-xs">
              {r}
            </code>
          ))
        )}
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
      <CardHeader
        agentId={approval.agent_id}
        upstream={approval.host || shortId(approval.upstream_id)}
        tag="new-value"
        createdAt={approval.created_at}
        actions={
          <>
            <button onClick={() => approve(false)} className={approveBtn}>
              Approve
            </button>
            <button onClick={() => approve(true)} className={trustBtn}>
              Approve + trust any
            </button>
            <button onClick={() => onResolve(approval.id, false)} className={denyBtn}>
              Deny
            </button>
          </>
        }
      />

      <HeroBox>
        <ScopeBadge scope={{ label: approval.method || '*', kind: 'method' }} />
        <span className="font-mono text-[13px] font-semibold">
          <Segments template={approval.template ?? ''} />
        </span>
      </HeroBox>

      <PurposeLine purpose={approval.purpose} />

      <div className="space-y-1">
        {newVals.map((nv) => (
          <div key={nv.var} className="flex items-center gap-2 text-xs">
            <span className="font-mono text-muted-foreground">{nv.var}</span>
            <span className="text-muted-foreground">=</span>
            <span className="rounded bg-primary/15 px-1 font-mono text-primary">{nv.value}</span>
          </div>
        ))}
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

export function ApprovalCard(props: CardProps) {
  const { approval } = props
  if (approval.kind === 'host-access') return <HostCard {...props} />
  if (approval.kind === 'operation') return <OperationCard {...props} />
  if (approval.kind === 'k8s-access') return <K8sAccessCard {...props} />
  if (approval.kind === 'preset') return <PresetCard {...props} />
  if ((approval.new_values?.length ?? 0) > 0) return <NewValueCard {...props} />
  return <PlainCard {...props} />
}
