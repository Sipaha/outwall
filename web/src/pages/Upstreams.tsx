import { useCallback, useEffect, useState } from 'react'
import { listUpstreams, createUpstream, ApiError } from '../lib/api'
import type { Upstream, UpstreamAuthConfig } from '../lib/types'
import { useEventStore } from '../lib/events'
import { DataTable } from '../components/DataTable'
import { StatusBadge } from '../components/StatusBadge'
import { Modal } from '../components/Modal'
import { FormField, fieldControlClass } from '../components/FormField'
import { Select } from '../components/Select'
import { useToastStore } from '../lib/toast'

export function Upstreams() {
  const [upstreams, setUpstreams] = useState<Upstream[]>([])
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [auth, setAuth] = useState<UpstreamAuthConfig>({ type: 'none' })
  const [busy, setBusy] = useState(false)
  const push = useToastStore((s) => s.push)

  const counter = useEventStore((s) => s.counters['upstream.created'])

  const load = useCallback(() => {
    listUpstreams()
      .then((u) => setUpstreams(u ?? []))
      .catch((err) => {
        push('error', err instanceof ApiError ? err.message : 'Failed to load upstreams')
      })
  }, [push])

  useEffect(load, [load, counter])

  function openModal() {
    setName('')
    setBaseURL('')
    setAuth({ type: 'none' })
    setOpen(true)
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await createUpstream(name, baseURL, auth)
      push('success', 'Upstream created')
      setOpen(false)
      load()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to create upstream')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Upstreams</h1>
        <button
          onClick={openModal}
          className="rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:opacity-90"
        >
          Add upstream
        </button>
      </div>

      <section className="rounded-lg border border-border bg-card">
        <DataTable
          rows={upstreams}
          rowKey={(u) => u.id}
          empty="No upstreams yet"
          columns={[
            { header: 'Name', cell: (u) => u.name },
            { header: 'Base URL', cell: (u) => u.base_url, className: 'font-mono text-muted-foreground' },
            { header: 'Auth', cell: (u) => <StatusBadge status={u.auth_type} /> },
          ]}
        />
      </section>

      <Modal
        open={open}
        title="Add upstream"
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
        <FormField label="Name">
          <input
            className={fieldControlClass}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="github"
            aria-label="Name"
          />
        </FormField>
        <FormField label="Base URL">
          <input
            className={fieldControlClass}
            value={baseURL}
            onChange={(e) => setBaseURL(e.target.value)}
            placeholder="https://api.github.com"
            aria-label="Base URL"
          />
        </FormField>

        <FormField label="Auth type">
          <Select
            value={auth.type}
            onChange={(t) => setAuth({ type: t })}
            options={[
              { value: 'none', label: 'None' },
              { value: 'static', label: 'Static header / API key' },
              { value: 'basic', label: 'Basic' },
              { value: 'oidc-client-credentials', label: 'OIDC client-credentials' },
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
        {auth.type === 'oidc-client-credentials' && (
          <>
            <FormField label="Token URL">
              <input
                className={fieldControlClass}
                value={auth.token_url ?? ''}
                onChange={(e) => setAuth({ ...auth, token_url: e.target.value })}
                aria-label="Token URL"
              />
            </FormField>
            <FormField label="Client ID">
              <input
                className={fieldControlClass}
                value={auth.client_id ?? ''}
                onChange={(e) => setAuth({ ...auth, client_id: e.target.value })}
                aria-label="Client ID"
              />
            </FormField>
            <FormField label="Client secret">
              <input
                className={fieldControlClass}
                type="password"
                value={auth.client_secret ?? ''}
                onChange={(e) => setAuth({ ...auth, client_secret: e.target.value })}
                aria-label="Client secret"
              />
            </FormField>
            <FormField label="Scope">
              <input
                className={fieldControlClass}
                value={auth.scope ?? ''}
                onChange={(e) => setAuth({ ...auth, scope: e.target.value })}
                aria-label="Scope"
              />
            </FormField>
          </>
        )}
      </Modal>
    </div>
  )
}
