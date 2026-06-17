import { useCallback, useEffect, useState } from 'react'
import { listAudit, getAudit, ApiError } from '../lib/api'
import type { AuditEntry, AuditDetail } from '../lib/types'
import { useEventStore } from '../lib/events'
import { DataTable } from '../components/DataTable'
import { StatusBadge } from '../components/StatusBadge'
import { Modal } from '../components/Modal'
import { JsonView } from '../components/JsonView'
import { useToastStore } from '../lib/toast'

function fmtTime(iso: string): string {
  const d = new Date(iso)
  return isNaN(d.getTime()) ? iso : d.toLocaleString()
}

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

export function Audit() {
  const [entries, setEntries] = useState<AuditEntry[]>([])
  const [detail, setDetail] = useState<AuditDetail | null>(null)
  const push = useToastStore((s) => s.push)

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

      <section className="rounded-lg border border-border bg-card">
        <DataTable
          rows={entries}
          rowKey={(e) => e.id}
          empty="No audit entries yet"
          columns={[
            { header: 'When', cell: (e) => fmtTime(e.ts), className: 'text-muted-foreground whitespace-nowrap' },
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

      <Modal
        open={detail !== null}
        title="Audit entry"
        onClose={() => setDetail(null)}
        width="lg"
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
