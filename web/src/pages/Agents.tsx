import { useCallback, useEffect, useState } from 'react'
import { listAgents, listRules, listAccessRequests, ApiError } from '../lib/api'
import type { Agent, Rule, AccessRequest } from '../lib/types'
import { useEventStore } from '../lib/events'
import { DataTable } from '../components/DataTable'
import { StatusBadge } from '../components/StatusBadge'
import { Modal } from '../components/Modal'
import { useToastStore } from '../lib/toast'

function shortId(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id
}

export function Agents() {
  const [agents, setAgents] = useState<Agent[]>([])
  const [rules, setRules] = useState<Rule[]>([])
  const [requests, setRequests] = useState<AccessRequest[]>([])
  const [selected, setSelected] = useState<Agent | null>(null)
  const push = useToastStore((s) => s.push)

  const counter = useEventStore((s) => s.counters['agent.registered'])

  const load = useCallback(() => {
    Promise.all([listAgents(), listRules(), listAccessRequests()])
      .then(([a, r, ar]) => {
        setAgents(a ?? [])
        setRules(r ?? [])
        setRequests(ar ?? [])
      })
      .catch((err) => {
        push('error', err instanceof ApiError ? err.message : 'Failed to load agents')
      })
  }, [push])

  useEffect(load, [load, counter])

  const agentRules = selected ? rules.filter((r) => r.subject_agent_id === selected.id) : []
  const agentRequests = selected ? requests.filter((r) => r.agent_id === selected.id) : []

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-lg font-semibold">Agents</h1>

      <section className="rounded-lg border border-border bg-card">
        <DataTable
          rows={agents}
          rowKey={(a) => a.id}
          empty="No agents yet"
          columns={[
            { header: 'Name', cell: (a) => a.name },
            { header: 'Status', cell: (a) => <StatusBadge status={a.status} /> },
            { header: 'ID', cell: (a) => shortId(a.id), className: 'font-mono text-muted-foreground' },
            {
              header: '',
              cell: (a) => (
                <div className="flex justify-end">
                  <button
                    onClick={() => setSelected(a)}
                    className="rounded bg-muted px-2 py-0.5 text-[11px] font-medium text-foreground hover:bg-muted/70"
                  >
                    Detail
                  </button>
                </div>
              ),
            },
          ]}
        />
      </section>

      <Modal
        open={selected !== null}
        title={selected ? `Agent · ${selected.name}` : 'Agent'}
        onClose={() => setSelected(null)}
        width="lg"
      >
        {selected && (
          <>
            <div className="text-xs text-muted-foreground">
              <span className="font-mono">{selected.id}</span> · <StatusBadge status={selected.status} />
            </div>

            <div>
              <h3 className="mb-1 text-xs font-semibold text-muted-foreground">Rules</h3>
              <DataTable
                rows={agentRules}
                rowKey={(r) => r.id}
                empty="No rules target this agent"
                columns={[
                  { header: 'Method', cell: (r) => r.method || '*', className: 'font-mono' },
                  { header: 'Path', cell: (r) => r.path_glob, className: 'font-mono' },
                  { header: 'Outcome', cell: (r) => <StatusBadge status={r.outcome} /> },
                ]}
              />
            </div>

            <div>
              <h3 className="mb-1 text-xs font-semibold text-muted-foreground">Access requests</h3>
              <DataTable
                rows={agentRequests}
                rowKey={(r) => r.id}
                empty="No access requests from this agent"
                columns={[
                  { header: 'Upstream', cell: (r) => r.upstream_name || shortId(r.upstream_id) },
                  { header: 'Purpose', cell: (r) => r.purpose },
                  { header: 'Status', cell: (r) => <StatusBadge status={r.status} /> },
                ]}
              />
            </div>
          </>
        )}
      </Modal>
    </div>
  )
}
