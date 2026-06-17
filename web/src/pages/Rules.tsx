import { useCallback, useEffect, useState } from 'react'
import {
  listRules,
  listUpstreams,
  listAgents,
  createRule,
  deleteRule,
  ApiError,
} from '../lib/api'
import type { Rule, Upstream, Agent } from '../lib/types'
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
  method: string
  path_glob: string
  outcome: string
  rate_limit_per_min: number
}

const emptyDraft: DraftRule = {
  subject_agent_id: '',
  upstream_id: '',
  method: '*',
  path_glob: '/**',
  outcome: 'allow',
  rate_limit_per_min: 0,
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

  const counter = useEventStore((s) => s.counters['rule.created'])

  const load = useCallback(() => {
    Promise.all([listRules(), listUpstreams(), listAgents()])
      .then(([r, u, a]) => {
        setRules(r ?? [])
        setUpstreams(u ?? [])
        setAgents(a ?? [])
      })
      .catch((err) => {
        push('error', err instanceof ApiError ? err.message : 'Failed to load rules')
      })
  }, [push])

  useEffect(load, [load, counter])

  const upstreamName = (id: string) => upstreams.find((u) => u.id === id)?.name ?? id
  const agentName = (id: string) => (id === '' ? 'any' : agents.find((a) => a.id === id)?.name ?? id)

  function openModal() {
    setDraft({ ...emptyDraft, upstream_id: upstreams[0]?.id ?? '' })
    setOpen(true)
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!draft.upstream_id) {
      push('error', 'Select an upstream')
      return
    }
    setBusy(true)
    try {
      await createRule(draft)
      push('success', 'Rule created')
      setOpen(false)
      load()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to create rule')
    } finally {
      setBusy(false)
    }
  }

  async function remove(rule: Rule) {
    try {
      await deleteRule(rule.id)
      push('success', 'Rule deleted')
      setConfirmDelete(null)
      load()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to delete rule')
    }
  }

  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Rules</h1>
        <button
          onClick={openModal}
          className="rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:opacity-90"
        >
          Add rule
        </button>
      </div>

      <section className="rounded-lg border border-border bg-card">
        <DataTable
          rows={rules}
          rowKey={(r) => r.id}
          empty="No rules yet — default-deny applies"
          columns={[
            { header: 'Subject', cell: (r) => agentName(r.subject_agent_id) },
            { header: 'Upstream', cell: (r) => upstreamName(r.upstream_id) },
            { header: 'Method', cell: (r) => (r.method === '*' || r.method === '' ? 'any' : r.method), className: 'font-mono' },
            { header: 'Path glob', cell: (r) => r.path_glob, className: 'font-mono' },
            { header: 'Outcome', cell: (r) => <StatusBadge status={r.outcome} /> },
            {
              header: 'Rate',
              cell: (r) => (r.rate_limit_per_min > 0 ? `${r.rate_limit_per_min}/min` : '∞'),
              className: 'font-mono text-muted-foreground',
            },
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

      <Modal
        open={open}
        title="Add rule"
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
        <FormField label="Upstream">
          <Select
            value={draft.upstream_id}
            onChange={(v) => setDraft({ ...draft, upstream_id: v })}
            options={upstreams.map((u) => ({ value: u.id, label: u.name }))}
          />
        </FormField>
        <FormField label="Method">
          <input
            className={fieldControlClass}
            value={draft.method}
            onChange={(e) => setDraft({ ...draft, method: e.target.value })}
            placeholder="*"
            aria-label="Method"
          />
        </FormField>
        <FormField label="Path glob">
          <input
            className={fieldControlClass}
            value={draft.path_glob}
            onChange={(e) => setDraft({ ...draft, path_glob: e.target.value })}
            placeholder="/**"
            aria-label="Path glob"
          />
        </FormField>
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
        title="Delete rule"
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
          Delete this rule? Removing it falls back to default-deny for matching requests.
        </p>
      </Modal>
    </div>
  )
}
