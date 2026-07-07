import { useEffect, useState } from 'react'
import { createRule, listUpstreams, listAgents, ApiError } from '../../lib/api'
import type { Rule, Upstream, Agent, ValuePolicy } from '../../lib/types'
import { Modal } from '../../components/Modal'
import { FormField, fieldControlClass } from '../../components/FormField'
import { Select } from '../../components/Select'
import { useToastStore } from '../../lib/toast'
import { DurationSelect, DEFAULT_TTL_SECONDS } from './DurationSelect'

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
  // server-profile Records fields
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

interface ManualRuleModalProps {
  open: boolean
  onClose: () => void
  onCreated: () => void
}

/** ManualRuleModal ("Выдать вручную") is the manual add-operation form for the Access page:
 *  it fetches upstreams + agents on open and lets the operator hand-craft an http operation,
 *  a k8s RBAC tuple, or a server-profile (Records) rule, mirroring the old Operations page's
 *  "Add operation" modal. */
export function ManualRuleModal({ open, onClose, onCreated }: ManualRuleModalProps) {
  const [upstreams, setUpstreams] = useState<Upstream[]>([])
  const [agents, setAgents] = useState<Agent[]>([])
  const [draft, setDraft] = useState<DraftRule>(emptyDraft)
  const [busy, setBusy] = useState(false)
  const [ttl, setTtl] = useState(DEFAULT_TTL_SECONDS)
  const push = useToastStore((s) => s.push)

  useEffect(() => {
    if (!open) return
    Promise.all([listUpstreams(), listAgents()])
      .then(([u, a]) => {
        setUpstreams(u ?? [])
        setAgents(a ?? [])
        // Default the verb to "*" so the k8s verb <select> is a controlled match from the start.
        setDraft({ ...emptyDraft, upstream_id: (u ?? [])[0]?.id ?? '', verb: '*' })
      })
      .catch((err) => {
        push('error', err instanceof ApiError ? err.message : 'Failed to load upstreams/agents')
      })
  }, [open, push])

  // The rule editor adapts to the selected upstream: k8s clusters match on the RBAC tuple
  // (namespace/resource/verb); http upstreams are operation rules (method + path-template +
  // per-variable value policies); profiled upstreams use Records fields (op/sourceId/workspace).
  const draftIsK8s = upstreams.find((u) => u.id === draft.upstream_id)?.kind === 'k8s'
  const draftProfile = upstreams.find((u) => u.id === draft.upstream_id)?.profile

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!draft.upstream_id) {
      push('error', 'Select a host')
      return
    }
    setBusy(true)
    try {
      // k8s rules send the RBAC tuple; profiled upstreams send a Records rule (op/sourceId/workspace);
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
          ttl_seconds: ttl,
        }
      } else if (draftProfile === 'citeck') {
        payload = {
          subject_agent_id: draft.subject_agent_id,
          upstream_id: draft.upstream_id,
          outcome: draft.outcome,
          rate_limit_per_min: draft.rate_limit_per_min,
          profile: 'citeck',
          profile_params: { op: draft.rec_op, source_id: draft.source_id, workspace: draft.workspace },
          ttl_seconds: ttl,
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
          ttl_seconds: ttl,
        }
      }
      await createRule(payload)
      push('success', 'Operation created')
      onCreated()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to create operation')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      title="Add operation"
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <button
            type="button"
            onClick={onClose}
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
      <FormField label="Grant duration">
        <DurationSelect value={ttl} onChange={setTtl} />
      </FormField>
    </Modal>
  )
}
