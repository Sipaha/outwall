import { useCallback, useEffect, useState } from 'react'
import {
  listRules,
  listUpstreams,
  listAgents,
  createRule,
  deleteRule,
  setRuleVariablePolicy,
  ApiError,
} from '../lib/api'
import type { Rule, Upstream, Agent, ValuePolicy } from '../lib/types'
import { useEventStore } from '../lib/events'
import { DataTable } from '../components/DataTable'
import { StatusBadge } from '../components/StatusBadge'
import { Modal } from '../components/Modal'
import { FormField, fieldControlClass } from '../components/FormField'
import { Select } from '../components/Select'
import { useToastStore } from '../lib/toast'

interface DraftRule {
  subject_agent_id: string
  upstream_id: string
  op_method: string
  op_path_template: string
  // op_values: one "var=value" per line; "var=*" trusts any value for that variable.
  op_values: string
  outcome: string
  rate_limit_per_min: number
  namespace: string
  resource: string
  verb: string
  // citeck Records fields
  rec_op: string
  source_id: string
  workspace: string
}

const emptyDraft: DraftRule = {
  subject_agent_id: '',
  upstream_id: '',
  op_method: 'GET',
  op_path_template: '',
  op_values: '',
  outcome: 'allow',
  rate_limit_per_min: 0,
  namespace: '',
  resource: '',
  verb: '',
  rec_op: 'read',
  source_id: '',
  workspace: '',
}

// parseOpValues turns "var=value" lines into per-variable text value policies. "var=*" sets the
// variable to mode "any" (trust any value); otherwise the value joins the variable's allowed-set.
function parseOpValues(text: string): Record<string, ValuePolicy> {
  const out: Record<string, ValuePolicy> = {}
  for (const raw of text.split('\n')) {
    const line = raw.trim()
    if (line === '') continue
    const eq = line.indexOf('=')
    if (eq <= 0) continue
    const name = line.slice(0, eq)
    const val = line.slice(eq + 1)
    const vp = out[name] ?? { type: 'text', mode: 'set', values: [] }
    if (val === '*') {
      vp.mode = 'any'
    } else if (vp.mode !== 'any') {
      vp.values = [...(vp.values ?? []), val]
    }
    out[name] = vp
  }
  return out
}

// k8s RBAC verbs offered in the rule editor (mirrors internal/k8s verbFor + policy.Rule.Verb).
const K8S_VERBS = ['*', 'get', 'list', 'watch', 'create', 'update', 'patch', 'delete', 'deletecollection']

// isOperationRule is true for an http operation rule (has a path-template); k8s rules carry the
// RBAC tuple instead and live in their own section.
function isOperationRule(r: Rule): boolean {
  return !!r.op_path_template
}

// segmentsOf splits a path template into fixed vs `{name:type}` variable pieces so the template can
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

interface ValueSetEditorProps {
  ruleID: string
  varName: string
  policy: ValuePolicy
  onChange: () => void
}

/** Per-text-variable editor: the allowed-value chips (each removable), an add-value input, and a
 *  "trust any value" toggle. Each action posts the whole recomputed policy via setRuleVariablePolicy. */
function ValueSetEditor({ ruleID, varName, policy, onChange }: ValueSetEditorProps) {
  const [draft, setDraft] = useState('')
  const push = useToastStore((s) => s.push)
  const isAny = policy.mode === 'any'
  const values = policy.values ?? []

  async function post(next: ValuePolicy) {
    try {
      await setRuleVariablePolicy(ruleID, varName, next)
      onChange()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to update value policy')
    }
  }

  function addValue() {
    const v = draft.trim()
    if (v === '') return
    setDraft('')
    void post({ type: 'text', mode: 'set', values: [...values, v] })
  }

  function removeValue(v: string) {
    void post({ type: 'text', mode: 'set', values: values.filter((x) => x !== v) })
  }

  function toggleAny(any: boolean) {
    void post(any ? { type: 'text', mode: 'any' } : { type: 'text', mode: 'set', values })
  }

  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-xs">
          {varName}
          <span className="ml-1 text-muted-foreground">:text</span>
        </span>
        <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <input
            type="checkbox"
            checked={isAny}
            onChange={(e) => toggleAny(e.target.checked)}
            aria-label={`Trust any value for ${varName}`}
          />
          trust any value
        </label>
      </div>
      {isAny ? (
        <div className="rounded border border-warning/40 bg-warning/10 px-2 py-1 text-[11px] text-warning">
          ⚠ Any value is allowed for {varName}.
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-1.5">
          {values.length === 0 && (
            <span className="text-[11px] text-muted-foreground">No values yet — none allowed.</span>
          )}
          {values.map((v) => (
            <span
              key={v}
              className="inline-flex items-center gap-1 rounded bg-primary/15 px-1.5 py-0.5 font-mono text-[11px] text-primary"
            >
              {v}
              <button
                onClick={() => removeValue(v)}
                aria-label={`Remove ${v} from ${varName}`}
                className="text-primary/70 hover:text-primary"
              >
                ×
              </button>
            </span>
          ))}
          <input
            className="w-40 rounded border border-border bg-muted px-2 py-0.5 text-[11px] focus:border-primary focus:outline-none"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                addValue()
              }
            }}
            placeholder="add a value…"
            aria-label={`Value to add for ${varName}`}
          />
          <button
            onClick={addValue}
            aria-label={`Add value for ${varName}`}
            className="rounded bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground hover:text-foreground"
          >
            Add
          </button>
        </div>
      )}
    </div>
  )
}

/** Per-enum-variable editor: a CLOSED allowed-set. Unlike text, a value outside the set is DENIED
 *  (the set does not auto-grow on request), so there is no "trust any" toggle. */
function EnumSetEditor({ ruleID, varName, policy, onChange }: ValueSetEditorProps) {
  const [draft, setDraft] = useState('')
  const push = useToastStore((s) => s.push)
  const values = policy.values ?? []

  async function post(next: ValuePolicy) {
    try {
      await setRuleVariablePolicy(ruleID, varName, next)
      onChange()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to update value policy')
    }
  }

  function addValue() {
    const v = draft.trim()
    if (v === '') return
    setDraft('')
    void post({ type: 'enum', mode: 'set', values: [...values, v] })
  }

  function removeValue(v: string) {
    void post({ type: 'enum', mode: 'set', values: values.filter((x) => x !== v) })
  }

  return (
    <div className="space-y-1.5">
      <span className="font-mono text-xs">
        {varName}
        <span className="ml-1 text-muted-foreground">:enum</span>
      </span>
      <div className="rounded border border-border/60 bg-muted/40 px-2 py-1 text-[11px] text-muted-foreground">
        Closed domain — a value not in this set is <span className="text-destructive">denied</span>.
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        {values.length === 0 && (
          <span className="text-[11px] text-muted-foreground">No values yet — none allowed.</span>
        )}
        {values.map((v) => (
          <span
            key={v}
            className="inline-flex items-center gap-1 rounded bg-primary/15 px-1.5 py-0.5 font-mono text-[11px] text-primary"
          >
            {v}
            <button
              onClick={() => removeValue(v)}
              aria-label={`Remove ${v} from ${varName}`}
              className="text-primary/70 hover:text-primary"
            >
              ×
            </button>
          </span>
        ))}
        <input
          className="w-40 rounded border border-border bg-muted px-2 py-0.5 text-[11px] focus:border-primary focus:outline-none"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              addValue()
            }
          }}
          placeholder="add an enum value…"
          aria-label={`Value to add for ${varName}`}
        />
        <button
          onClick={addValue}
          aria-label={`Add value for ${varName}`}
          className="rounded bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground hover:text-foreground"
        >
          Add
        </button>
      </div>
    </div>
  )
}

/** Per-number-variable editor: an inclusive [min,max] range, or "any number". */
function NumberRangeEditor({ ruleID, varName, policy, onChange }: ValueSetEditorProps) {
  const push = useToastStore((s) => s.push)
  const isAny = policy.mode === 'any'

  async function post(next: ValuePolicy) {
    try {
      await setRuleVariablePolicy(ruleID, varName, next)
      onChange()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to update value policy')
    }
  }

  function setBound(which: 'min' | 'max', raw: string) {
    const n = raw.trim() === '' ? undefined : Number(raw)
    if (n !== undefined && Number.isNaN(n)) return
    void post({ type: 'number', mode: 'range', min: which === 'min' ? n : policy.min, max: which === 'max' ? n : policy.max })
  }

  function toggleAny(any: boolean) {
    void post(any ? { type: 'number', mode: 'any' } : { type: 'number', mode: 'range', min: policy.min, max: policy.max })
  }

  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-xs">
          {varName}
          <span className="ml-1 text-muted-foreground">:number</span>
        </span>
        <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <input
            type="checkbox"
            checked={isAny}
            onChange={(e) => toggleAny(e.target.checked)}
            aria-label={`Allow any number for ${varName}`}
          />
          any number
        </label>
      </div>
      {isAny ? (
        <div className="rounded border border-warning/40 bg-warning/10 px-2 py-1 text-[11px] text-warning">
          ⚠ Any number is allowed for {varName}.
        </div>
      ) : (
        <div className="flex items-center gap-2 text-[11px]">
          <label className="flex items-center gap-1">
            min
            <input
              type="number"
              className="w-24 rounded border border-border bg-muted px-2 py-0.5 focus:border-primary focus:outline-none"
              defaultValue={policy.min ?? ''}
              onBlur={(e) => setBound('min', e.target.value)}
              aria-label={`Minimum for ${varName}`}
            />
          </label>
          <label className="flex items-center gap-1">
            max
            <input
              type="number"
              className="w-24 rounded border border-border bg-muted px-2 py-0.5 focus:border-primary focus:outline-none"
              defaultValue={policy.max ?? ''}
              onBlur={(e) => setBound('max', e.target.value)}
              aria-label={`Maximum for ${varName}`}
            />
          </label>
          <span className="text-muted-foreground">out-of-range → denied</span>
        </div>
      )}
    </div>
  )
}

export function Rules() {
  const [rules, setRules] = useState<Rule[]>([])
  const [upstreams, setUpstreams] = useState<Upstream[]>([])
  const [agents, setAgents] = useState<Agent[]>([])
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<DraftRule>(emptyDraft)
  const [busy, setBusy] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<Rule | null>(null)
  const push = useToastStore((s) => s.push)

  const counters = useEventStore((s) => s.counters)
  const counter = (counters['rule.created'] ?? 0) + (counters['rule.updated'] ?? 0)

  const load = useCallback(() => {
    Promise.all([listRules(), listUpstreams(), listAgents()])
      .then(([r, u, a]) => {
        setRules(r ?? [])
        setUpstreams(u ?? [])
        setAgents(a ?? [])
      })
      .catch((err) => {
        push('error', err instanceof ApiError ? err.message : 'Failed to load operations')
      })
  }, [push])

  useEffect(load, [load, counter])

  const upstreamName = (id: string) => upstreams.find((u) => u.id === id)?.name ?? id
  const agentName = (id: string) => (id === '' ? 'any' : agents.find((a) => a.id === id)?.name ?? id)

  const operationRules = rules.filter(isOperationRule)
  const k8sRules = rules.filter((r) => !isOperationRule(r) && (r.namespace || r.resource || r.verb))

  // The rule editor adapts to the selected upstream: k8s clusters match on the RBAC tuple
  // (namespace/resource/verb); http upstreams are operation rules (method + path-template +
  // per-variable value policies); citeck upstreams use Records fields (op/sourceId/workspace).
  const draftIsK8s = upstreams.find((u) => u.id === draft.upstream_id)?.kind === 'k8s'
  const draftProfile = upstreams.find((u) => u.id === draft.upstream_id)?.profile

  function openModal() {
    // Default the verb to "*" so the k8s verb <select> is a controlled match from the start.
    setDraft({ ...emptyDraft, upstream_id: upstreams[0]?.id ?? '', verb: '*' })
    setOpen(true)
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!draft.upstream_id) {
      push('error', 'Select a host')
      return
    }
    setBusy(true)
    try {
      // k8s rules send the RBAC tuple; citeck upstreams send a Records rule (op/sourceId/workspace);
      // http rules are operation rules (method + path-template + per-variable value policies).
      let payload: Omit<Rule, 'id'>
      if (draftIsK8s) {
        payload = {
          subject_agent_id: draft.subject_agent_id,
          upstream_id: draft.upstream_id,
          outcome: draft.outcome,
          rate_limit_per_min: draft.rate_limit_per_min,
          namespace: draft.namespace,
          resource: draft.resource,
          verb: draft.verb,
        }
      } else if (draftProfile === 'citeck') {
        payload = {
          subject_agent_id: draft.subject_agent_id,
          upstream_id: draft.upstream_id,
          outcome: draft.outcome,
          rate_limit_per_min: draft.rate_limit_per_min,
          profile: 'citeck',
          profile_params: { op: draft.rec_op, source_id: draft.source_id, workspace: draft.workspace },
        }
      } else {
        payload = {
          subject_agent_id: draft.subject_agent_id,
          upstream_id: draft.upstream_id,
          op_method: draft.op_method,
          op_path_template: draft.op_path_template,
          op_value_policies: parseOpValues(draft.op_values),
          outcome: draft.outcome,
          rate_limit_per_min: draft.rate_limit_per_min,
        }
      }
      await createRule(payload)
      push('success', 'Operation created')
      setOpen(false)
      load()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to create operation')
    } finally {
      setBusy(false)
    }
  }

  async function remove(rule: Rule) {
    try {
      await deleteRule(rule.id)
      push('success', 'Operation deleted')
      setConfirmDelete(null)
      load()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to delete operation')
    }
  }

  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Operations</h1>
        <button
          onClick={openModal}
          className="rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:opacity-90"
        >
          Add operation
        </button>
      </div>

      {/* Operation templates: each with its per-variable value-sets. */}
      <section className="space-y-2">
        <header className="text-xs font-semibold text-muted-foreground">Operation templates</header>
        {operationRules.length === 0 ? (
          <div className="rounded-lg border border-border bg-card px-3 py-6 text-center text-xs text-muted-foreground">
            No operations yet — default-deny applies
          </div>
        ) : (
          <div className="space-y-2">
            {operationRules.map((r) => {
              const policies = r.op_value_policies ?? {}
              const textVars = Object.entries(policies).filter(([, p]) => p.type === 'text')
              const dateVars = Object.entries(policies).filter(([, p]) => p.type === 'date')
              const numberVars = Object.entries(policies).filter(([, p]) => p.type === 'number')
              const enumVars = Object.entries(policies).filter(([, p]) => p.type === 'enum')
              return (
                <div key={r.id} className="rounded-lg border border-border bg-card p-3 space-y-3">
                  <div className="flex items-start justify-between gap-2">
                    <div className="space-y-1">
                      <div className="text-[11px] text-muted-foreground">
                        {upstreamName(r.upstream_id)} · subject {agentName(r.subject_agent_id)}
                      </div>
                      <div className="font-mono text-xs">
                        <span className="font-semibold">{(r.op_method || '*') + ' '}</span>
                        {segmentsOf(r.op_path_template ?? '').map((s, i) => (
                          <span
                            key={i}
                            className={
                              s.variable
                                ? 'rounded bg-primary/15 px-1 text-primary'
                                : 'text-foreground'
                            }
                          >
                            {s.text}
                          </span>
                        ))}
                        {Object.entries(r.op_query_template ?? {}).map(([k, v], i) => (
                          <span key={k}>
                            {i === 0 ? '?' : '&'}
                            {k}=
                            {segmentsOf(v).map((s, j) => (
                              <span
                                key={j}
                                className={
                                  s.variable
                                    ? 'rounded bg-primary/15 px-1 text-primary'
                                    : 'text-foreground'
                                }
                              >
                                {s.text}
                              </span>
                            ))}
                          </span>
                        ))}
                      </div>
                      {Object.keys(r.op_body_template ?? {}).length > 0 && (
                        <div className="font-mono text-[11px] text-muted-foreground">
                          body:{' '}
                          {Object.entries(r.op_body_template ?? {}).map(([path, v], i) => (
                            <span key={path}>
                              {i > 0 && ', '}
                              {path}=
                              <span className="rounded bg-primary/15 px-1 text-primary">{v}</span>
                            </span>
                          ))}
                        </div>
                      )}
                    </div>
                    <div className="flex items-center gap-2">
                      <StatusBadge status={r.outcome} />
                      <button
                        onClick={() => setConfirmDelete(r)}
                        className="rounded bg-destructive/15 px-2 py-0.5 text-[11px] font-medium text-destructive hover:bg-destructive/25"
                      >
                        Delete
                      </button>
                    </div>
                  </div>

                  {textVars.length > 0 && (
                    <div className="space-y-2 border-t border-border/60 pt-2">
                      {textVars.map(([name, p]) => (
                        <ValueSetEditor
                          key={name}
                          ruleID={r.id}
                          varName={name}
                          policy={p}
                          onChange={load}
                        />
                      ))}
                    </div>
                  )}
                  {numberVars.length > 0 && (
                    <div className="space-y-2 border-t border-border/60 pt-2">
                      {numberVars.map(([name, p]) => (
                        <NumberRangeEditor key={name} ruleID={r.id} varName={name} policy={p} onChange={load} />
                      ))}
                    </div>
                  )}
                  {enumVars.length > 0 && (
                    <div className="space-y-2 border-t border-border/60 pt-2">
                      {enumVars.map(([name, p]) => (
                        <EnumSetEditor key={name} ruleID={r.id} varName={name} policy={p} onChange={load} />
                      ))}
                    </div>
                  )}
                  {dateVars.length > 0 && (
                    <div className="text-[11px] text-muted-foreground">
                      {dateVars.map(([name]) => (
                        <span key={name} className="mr-3 font-mono">
                          {name}:date <span className="text-muted-foreground">auto (any date)</span>
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </section>

      {/* k8s clusters keep their RBAC-tuple rules in a separate list. */}
      {k8sRules.length > 0 && (
        <section className="rounded-lg border border-border bg-card">
          <header className="border-b border-border px-3 py-2 text-xs font-semibold text-muted-foreground">
            Cluster (k8s) rules
          </header>
          <DataTable
            rows={k8sRules}
            rowKey={(r) => r.id}
            empty="No cluster rules"
            columns={[
              { header: 'Subject', cell: (r) => agentName(r.subject_agent_id) },
              { header: 'Cluster', cell: (r) => upstreamName(r.upstream_id) },
              {
                header: 'Match',
                cell: (r) => (
                  <span className="font-mono">
                    {(r.namespace || '*') + '/' + (r.resource || '*')}{' '}
                    <span className="text-muted-foreground">{r.verb || '*'}</span>
                  </span>
                ),
              },
              { header: 'Outcome', cell: (r) => <StatusBadge status={r.outcome} /> },
              {
                header: '',
                cell: (r) => (
                  <div className="flex justify-end">
                    <button
                      onClick={() => setConfirmDelete(r)}
                      className="rounded bg-destructive/15 px-2 py-0.5 text-[11px] font-medium text-destructive hover:bg-destructive/25"
                    >
                      Delete
                    </button>
                  </div>
                ),
              },
            ]}
          />
        </section>
      )}

      <Modal
        open={open}
        title="Add operation"
        onClose={() => setOpen(false)}
        onSubmit={submit}
        footer={
          <>
            <button
              type="button"
              onClick={() => setOpen(false)}
              className="rounded px-3 py-1.5 text-xs text-muted-foreground hover:text-foreground"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={busy}
              className="rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
            >
              {busy ? '…' : 'Create'}
            </button>
          </>
        }
      >
        <FormField label="Subject">
          <Select
            value={draft.subject_agent_id}
            onChange={(v) => setDraft({ ...draft, subject_agent_id: v })}
            options={[
              { value: '', label: 'Any' },
              ...agents.map((a) => ({ value: a.id, label: a.name })),
            ]}
          />
        </FormField>
        <FormField label="Host">
          <Select
            value={draft.upstream_id}
            onChange={(v) => setDraft({ ...draft, upstream_id: v })}
            options={upstreams.map((u) => ({ value: u.id, label: u.name }))}
          />
        </FormField>
        {draftIsK8s ? (
          <>
            <FormField label="Namespace">
              <input
                className={fieldControlClass}
                value={draft.namespace}
                onChange={(e) => setDraft({ ...draft, namespace: e.target.value })}
                placeholder="prod, prod-*, *"
                aria-label="Namespace"
              />
            </FormField>
            <FormField label="Resource">
              <input
                className={fieldControlClass}
                value={draft.resource}
                onChange={(e) => setDraft({ ...draft, resource: e.target.value })}
                placeholder="deployments, pods/log, *"
                aria-label="Resource"
              />
            </FormField>
            <FormField label="Verb">
              <Select
                value={draft.verb}
                onChange={(v) => setDraft({ ...draft, verb: v })}
                options={K8S_VERBS.map((v) => ({ value: v, label: v }))}
              />
            </FormField>
          </>
        ) : draftProfile === 'citeck' ? (
          <>
            <FormField label="Records operation">
              <select
                className={fieldControlClass}
                value={draft.rec_op}
                onChange={(e) => setDraft({ ...draft, rec_op: e.target.value })}
                aria-label="Records operation"
              >
                <option value="read">read (query)</option>
                <option value="write">write (mutate/delete)</option>
              </select>
            </FormField>
            <FormField label="Source ID">
              <input
                className={fieldControlClass}
                value={draft.source_id}
                onChange={(e) => setDraft({ ...draft, source_id: e.target.value })}
                placeholder="emodel/type or *"
                aria-label="Source ID"
              />
            </FormField>
            <FormField label="Workspace">
              <input
                className={fieldControlClass}
                value={draft.workspace}
                onChange={(e) => setDraft({ ...draft, workspace: e.target.value })}
                placeholder="* (not enforced for update/delete)"
                aria-label="Workspace"
              />
            </FormField>
          </>
        ) : (
          <>
            <FormField label="Method">
              <input
                className={fieldControlClass}
                value={draft.op_method}
                onChange={(e) => setDraft({ ...draft, op_method: e.target.value })}
                placeholder="GET"
                aria-label="Method"
              />
            </FormField>
            <FormField label="Operation path-template">
              <input
                className={fieldControlClass}
                value={draft.op_path_template}
                onChange={(e) => setDraft({ ...draft, op_path_template: e.target.value })}
                placeholder="/projects/{project_path:text}/pipelines"
                aria-label="Operation path-template"
              />
            </FormField>
            <FormField label="Allowed values (one var=value per line; var=* trusts any)">
              <textarea
                className={fieldControlClass}
                value={draft.op_values}
                onChange={(e) => setDraft({ ...draft, op_values: e.target.value })}
                placeholder={'project_path=infra/helm\nproject_path=infra/charts'}
                aria-label="Allowed values"
                rows={3}
              />
            </FormField>
          </>
        )}
        <FormField label="Outcome">
          <Select
            value={draft.outcome}
            onChange={(v) => setDraft({ ...draft, outcome: v })}
            options={[
              { value: 'allow', label: 'allow' },
              { value: 'deny', label: 'deny' },
              { value: 'require-approval', label: 'require-approval' },
            ]}
          />
        </FormField>
        <FormField label="Rate limit (per min, 0 = unlimited)">
          <input
            className={fieldControlClass}
            type="number"
            min={0}
            value={draft.rate_limit_per_min}
            onChange={(e) => setDraft({ ...draft, rate_limit_per_min: Number(e.target.value) || 0 })}
            aria-label="Rate limit"
          />
        </FormField>
      </Modal>

      <Modal
        open={confirmDelete !== null}
        title="Delete operation"
        onClose={() => setConfirmDelete(null)}
        width="sm"
        footer={
          <>
            <button
              type="button"
              onClick={() => setConfirmDelete(null)}
              className="rounded px-3 py-1.5 text-xs text-muted-foreground hover:text-foreground"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={() => confirmDelete && remove(confirmDelete)}
              className="rounded bg-destructive px-3 py-1.5 text-xs font-medium text-white hover:opacity-90"
            >
              Delete
            </button>
          </>
        }
      >
        <p className="text-sm">
          Delete this operation? Removing it falls back to default-deny for matching requests.
        </p>
      </Modal>
    </div>
  )
}
