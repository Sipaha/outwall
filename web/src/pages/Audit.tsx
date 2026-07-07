import { useCallback, useEffect, useState } from 'react'
import { useSearchParams } from 'react-router'
import { listAudit, getAudit, listAccessRequests, ApiError } from '../lib/api'
import type { AuditEntry, AuditDetail, AccessRequest } from '../lib/types'
import { useEventStore } from '../lib/events'
import { DataTable } from '../components/DataTable'
import { StatusBadge } from '../components/StatusBadge'
import { Tabs } from '../components/Tabs'
import { Modal } from '../components/Modal'
import { JsonView } from '../components/JsonView'
import { RelTime } from '../components/RelTime'
import { useToastStore } from '../lib/toast'

type Tab = 'traffic' | 'requests'

const AUDIT_TABS = [
  { id: 'traffic', label: 'Трафик' },
  { id: 'requests', label: 'Запросы прав' },
]

function statusClass(code: number): string {
  if (code >= 500) return 'text-destructive'
  if (code >= 400) return 'text-warning'
  if (code >= 200 && code < 300) return 'text-success'
  return 'text-muted-foreground'
}

function MetaRow({ label, value }: { label: string; value: React.ReactNode }) {
  if (value === '' || value == null) return null
  return (
    <div>
      <dt className="text-[11px] text-muted-foreground">{label}</dt>
      <dd className="text-xs">{value}</dd>
    </div>
  )
}

/** Read-only history of access-request intents (the "Запросы прав" tab). No actions here — the
 *  live grant actions (revoke, resolve) now live on the Access page; this is audit history only. */
function AccessRequestsPanel({ agentFilter }: { agentFilter: string | null }) {
  const [requests, setRequests] = useState<AccessRequest[]>([])
  const push = useToastStore((s) => s.push)

  const counters = useEventStore((s) => s.counters)
  const counter = (counters['access.requested'] ?? 0) + (counters['access.revoked'] ?? 0)

  const load = useCallback(() => {
    listAccessRequests()
      .then((r) => setRequests(r ?? []))
      .catch((err) => {
        push('error', err instanceof ApiError ? err.message : 'Failed to load access requests')
      })
  }, [push])

  useEffect(load, [load, counter])

  const rows = agentFilter ? requests.filter((r) => r.agent_id === agentFilter) : requests

  return (
    <section className="rounded-lg border border-border bg-card">
      <DataTable
        rows={rows}
        rowKey={(r) => r.id}
        empty="No access requests yet"
        columns={[
          { header: 'Agent', cell: (r) => r.agent_name || r.agent_id },
          { header: 'Upstream', cell: (r) => r.upstream_name || r.upstream_id },
          { header: 'Purpose', cell: (r) => r.purpose, className: 'max-w-xs truncate' },
          {
            header: 'Status',
            cell: (r) => (
              <div className="flex flex-col gap-0.5">
                <StatusBadge status={r.status} />
                {r.status === 'denied' && r.reason && (
                  <span className="text-[11px] text-muted-foreground">{r.reason}</span>
                )}
              </div>
            ),
          },
          { header: 'Requested', cell: (r) => <RelTime iso={r.created_at} />, className: 'text-muted-foreground whitespace-nowrap' },
          {
            header: 'Resolved',
            cell: (r) => <RelTime iso={r.resolved_at} empty="—" />,
            className: 'text-muted-foreground whitespace-nowrap',
          },
        ]}
      />
    </section>
  )
}

export function Audit() {
  const [searchParams] = useSearchParams()
  const [tab, setTab] = useState<Tab>(searchParams.get('tab') === 'requests' ? 'requests' : 'traffic')
  const [entries, setEntries] = useState<AuditEntry[]>([])
  const [detail, setDetail] = useState<AuditDetail | null>(null)
  const push = useToastStore((s) => s.push)

  const agentFilter = searchParams.get('agent')

  const counter = useEventStore((s) => s.counters['audit.recorded'])

  const load = useCallback(() => {
    listAudit(200)
      .then((e) => setEntries(e ?? []))
      .catch((err) => {
        push('error', err instanceof ApiError ? err.message : 'Failed to load audit')
      })
  }, [push])

  useEffect(load, [load, counter])

  function openDetail(id: string) {
    getAudit(id)
      .then((d) => setDetail(d))
      .catch((err) => {
        push('error', err instanceof ApiError ? err.message : 'Failed to load audit entry')
      })
  }

  const reqBody = detail?.bodies.find((b) => b.kind === 'request')
  const respBody = detail?.bodies.find((b) => b.kind === 'response')

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-lg font-semibold">Audit</h1>

      <Tabs tabs={AUDIT_TABS} active={tab} onChange={(id) => setTab(id as Tab)} />

      {tab === 'traffic' ? (
        <section className="rounded-lg border border-border bg-card">
          <DataTable
            rows={entries}
            rowKey={(e) => e.id}
            empty="No audit entries yet"
            columns={[
              { header: 'When', cell: (e) => <RelTime iso={e.ts} />, className: 'text-muted-foreground whitespace-nowrap' },
              { header: 'Agent', cell: (e) => e.agent_name || e.agent_id },
              { header: 'Upstream', cell: (e) => e.upstream_name || e.upstream_id },
              { header: 'Method', cell: (e) => e.method, className: 'font-mono' },
              { header: 'Path', cell: (e) => e.path, className: 'font-mono max-w-xs truncate' },
              {
                header: 'Status',
                cell: (e) => <span className={`font-mono ${statusClass(e.status_code)}`}>{e.status_code || '—'}</span>,
              },
              { header: 'Dur', cell: (e) => `${e.duration_ms}ms`, className: 'font-mono text-muted-foreground' },
              { header: 'Decision', cell: (e) => <StatusBadge status={e.decision} /> },
              {
                header: '',
                cell: (e) => (
                  <div className="flex justify-end">
                    <button
                      onClick={() => openDetail(e.id)}
                      className="rounded bg-muted px-2 py-0.5 text-[11px] font-medium text-foreground hover:bg-muted/70"
                    >
                      View
                    </button>
                  </div>
                ),
              },
            ]}
          />
        </section>
      ) : (
        <AccessRequestsPanel agentFilter={agentFilter} />
      )}

      <Modal
        open={detail !== null}
        title="Audit entry"
        onClose={() => setDetail(null)}
        width="xl"
      >
        {detail && (
          <>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-2">
              <MetaRow label="Agent" value={detail.agent_name || detail.agent_id} />
              <MetaRow label="Upstream" value={detail.upstream_name || detail.upstream_id} />
              <MetaRow label="Method" value={<span className="font-mono">{detail.method}</span>} />
              <MetaRow
                label="Path"
                value={<span className="font-mono break-all">{detail.path}{detail.query ? `?${detail.query}` : ''}</span>}
              />
              <MetaRow
                label="Status"
                value={<span className={`font-mono ${statusClass(detail.status_code)}`}>{detail.status_code}</span>}
              />
              <MetaRow label="Duration" value={`${detail.duration_ms}ms`} />
              <MetaRow label="Sizes" value={`${detail.req_bytes} → ${detail.resp_bytes} bytes`} />
              <MetaRow label="Decision" value={<StatusBadge status={detail.decision} />} />
              <MetaRow label="Rule" value={detail.rule_id && <span className="font-mono">{detail.rule_id}</span>} />
              <MetaRow label="Error" value={detail.error && <span className="text-destructive">{detail.error}</span>} />
            </dl>

            <div>
              <h3 className="mb-1 text-xs font-semibold text-muted-foreground">Headers (masked)</h3>
              {Object.keys(detail.headers ?? {}).length === 0 ? (
                <div className="text-xs text-muted-foreground">No headers captured</div>
              ) : (
                <table className="w-full border-collapse text-xs">
                  <tbody>
                    {Object.entries(detail.headers).map(([k, v]) => (
                      <tr key={k} className="border-b border-border/60">
                        <td className="py-1 pr-3 font-mono text-muted-foreground align-top">{k}</td>
                        <td className="py-1 font-mono break-all">{v}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>

            <div>
              <h3 className="mb-1 text-xs font-semibold text-muted-foreground">Request body</h3>
              {reqBody ? (
                <JsonView body={reqBody} />
              ) : (
                <div className="text-xs text-muted-foreground">No request body</div>
              )}
            </div>

            <div>
              <h3 className="mb-1 text-xs font-semibold text-muted-foreground">Response body</h3>
              {respBody ? (
                <JsonView body={respBody} />
              ) : (
                <div className="text-xs text-muted-foreground">No response body</div>
              )}
            </div>
          </>
        )}
      </Modal>
    </div>
  )
}
