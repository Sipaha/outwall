import { useCallback, useEffect, useState } from 'react'
import {
  listUpstreams,
  createUpstream,
  setUpstreamAuth,
  deleteUpstream,
  ApiError,
} from '../lib/api'
import type { Upstream, UpstreamAuthConfig } from '../lib/types'
import { useEventStore } from '../lib/events'
import { DataTable } from '../components/DataTable'
import { Modal } from '../components/Modal'
import { FormField, fieldControlClass } from '../components/FormField'
import { Select } from '../components/Select'
import { useToastStore } from '../lib/toast'

/** True when the host carries a (non-"none") credential — drives the credential-status badge. */
function hasCredential(u: Upstream): boolean {
  return u.auth_type !== '' && u.auth_type !== 'none'
}

interface AuthFieldsProps {
  auth: UpstreamAuthConfig
  setAuth: (a: UpstreamAuthConfig) => void
}

/** The auth-type <select> + the conditional credential fields, shared by the add-host and
 *  set-credential modals (static header/value, basic user/pass, oidc client-credentials). */
function AuthFields({ auth, setAuth }: AuthFieldsProps) {
  return (
    <>
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
    </>
  )
}

export function Upstreams() {
  const [upstreams, setUpstreams] = useState<Upstream[]>([])
  const [addOpen, setAddOpen] = useState(false)
  const [name, setName] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [auth, setAuth] = useState<UpstreamAuthConfig>({ type: 'none' })
  const [busy, setBusy] = useState(false)
  // The host whose credential is being set/replaced (set-credential modal), and a separate auth draft.
  const [credFor, setCredFor] = useState<Upstream | null>(null)
  const [credAuth, setCredAuth] = useState<UpstreamAuthConfig>({ type: 'static' })
  const [confirmRemove, setConfirmRemove] = useState<Upstream | null>(null)
  const push = useToastStore((s) => s.push)

  const counters = useEventStore((s) => s.counters)
  const counter =
    (counters['upstream.created'] ?? 0) +
    (counters['upstream.updated'] ?? 0) +
    (counters['upstream.deleted'] ?? 0)

  const load = useCallback(() => {
    listUpstreams()
      // k8s clusters live on the dedicated Clusters screen — keep this list to http hosts.
      .then((u) => setUpstreams((u ?? []).filter((up) => up.kind !== 'k8s')))
      .catch((err) => {
        push('error', err instanceof ApiError ? err.message : 'Failed to load hosts')
      })
  }, [push])

  useEffect(load, [load, counter])

  function openAdd() {
    setName('')
    setBaseURL('')
    setAuth({ type: 'none' })
    setAddOpen(true)
  }

  async function submitAdd(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await createUpstream(name, baseURL, auth)
      push('success', 'Host created')
      setAddOpen(false)
      load()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to create host')
    } finally {
      setBusy(false)
    }
  }

  function openCred(u: Upstream) {
    setCredFor(u)
    setCredAuth({ type: 'static' })
  }

  async function submitCred(e: React.FormEvent) {
    e.preventDefault()
    if (!credFor) return
    setBusy(true)
    try {
      await setUpstreamAuth(credFor.name, credAuth)
      push('success', 'Credential saved')
      setCredFor(null)
      load()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to save credential')
    } finally {
      setBusy(false)
    }
  }

  async function remove(u: Upstream) {
    try {
      await deleteUpstream(u.name)
      push('success', 'Host removed')
      setConfirmRemove(null)
      load()
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to remove host')
    }
  }

  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Hosts</h1>
        <button
          onClick={openAdd}
          className="rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:opacity-90"
        >
          Add host
        </button>
      </div>

      <section className="rounded-lg border border-border bg-card">
        <DataTable
          rows={upstreams}
          rowKey={(u) => u.id}
          empty="No hosts yet — they appear when an agent first requests access"
          columns={[
            { header: 'Host', cell: (u) => u.name },
            {
              header: 'Base URL',
              cell: (u) => u.base_url,
              className: 'font-mono text-muted-foreground',
            },
            {
              header: 'Credential',
              cell: (u) =>
                hasCredential(u) ? (
                  <span className="rounded bg-success/15 px-1.5 py-0.5 text-[11px] font-medium text-success">
                    credential set ({u.auth_type})
                  </span>
                ) : (
                  <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] font-medium text-muted-foreground">
                    no credential
                  </span>
                ),
            },
            {
              header: '',
              cell: (u) => (
                <div className="flex justify-end gap-1.5">
                  <button
                    onClick={() => openCred(u)}
                    aria-label={`Set credential for ${u.name}`}
                    className="rounded bg-primary/15 px-2 py-0.5 text-[11px] font-medium text-primary hover:bg-primary/25"
                  >
                    {hasCredential(u) ? 'Replace credential' : 'Set credential'}
                  </button>
                  <button
                    onClick={() => setConfirmRemove(u)}
                    aria-label={`Remove ${u.name}`}
                    className="rounded bg-destructive/15 px-2 py-0.5 text-[11px] font-medium text-destructive hover:bg-destructive/25"
                  >
                    Remove
                  </button>
                </div>
              ),
            },
          ]}
        />
      </section>

      <Modal
        open={addOpen}
        title="Add host"
        onClose={() => setAddOpen(false)}
        onSubmit={submitAdd}
        footer={
          <>
            <button
              type="button"
              onClick={() => setAddOpen(false)}
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
            placeholder="api.github.com"
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
        <AuthFields auth={auth} setAuth={setAuth} />
      </Modal>

      <Modal
        open={credFor !== null}
        title={credFor ? `Credential for ${credFor.name}` : 'Credential'}
        onClose={() => setCredFor(null)}
        onSubmit={submitCred}
        footer={
          <>
            <button
              type="button"
              onClick={() => setCredFor(null)}
              className="rounded px-3 py-1.5 text-xs text-muted-foreground hover:text-foreground"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={busy}
              className="rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
            >
              {busy ? '…' : 'Save'}
            </button>
          </>
        }
      >
        <p className="text-[11px] text-muted-foreground">
          The credential is encrypted in the vault and injected server-side — the agent never sees it.
        </p>
        {/* Render the fields only while the modal is open so the closed dialog leaks no inputs. */}
        {credFor !== null && <AuthFields auth={credAuth} setAuth={setCredAuth} />}
      </Modal>

      <Modal
        open={confirmRemove !== null}
        title="Remove host"
        onClose={() => setConfirmRemove(null)}
        width="sm"
        footer={
          <>
            <button
              type="button"
              onClick={() => setConfirmRemove(null)}
              className="rounded px-3 py-1.5 text-xs text-muted-foreground hover:text-foreground"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={() => confirmRemove && remove(confirmRemove)}
              className="rounded bg-destructive px-3 py-1.5 text-xs font-medium text-white hover:opacity-90"
            >
              Remove
            </button>
          </>
        }
      >
        <p className="text-sm">
          Remove {confirmRemove?.name}? Its operations and stored credential are deleted.
        </p>
      </Modal>
    </div>
  )
}
