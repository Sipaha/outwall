import { useCallback, useEffect, useState } from 'react'
import {
  listUpstreams,
  createUpstream,
  getProfiles,
  setUpstreamAuth,
  deleteUpstream,
  oauthLogin,
  discoverOIDC,
  oidcRedirectURI,
  ApiError,
} from '../lib/api'
import type { Upstream, UpstreamAuthConfig, ProfileSchema } from '../lib/types'
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
  const push = useToastStore((s) => s.push)
  const [issuer, setIssuer] = useState('')
  const [discovering, setDiscovering] = useState(false)
  const [redirectURI, setRedirectURI] = useState('')

  // The IdP redirect URI to register is fixed per daemon; fetch it when the OIDC form is shown.
  useEffect(() => {
    if (auth.type !== 'oidc-authorization-code') return
    let live = true
    oidcRedirectURI()
      .then((r) => live && setRedirectURI(r.redirect_uri))
      .catch(() => {})
    return () => {
      live = false
    }
  }, [auth.type])

  // discover fetches the OIDC well-known document for the issuer URL and fills the endpoints + scope.
  async function discover() {
    if (!issuer.trim()) return
    setDiscovering(true)
    try {
      const d = await discoverOIDC(issuer.trim())
      setAuth({
        ...auth,
        auth_url: d.authorization_endpoint,
        token_url: d.token_endpoint,
        scope: auth.scope || (d.scopes_supported?.includes('openid') ? 'openid profile' : auth.scope),
      })
      push('success', 'OIDC endpoints discovered')
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Discovery failed')
    } finally {
      setDiscovering(false)
    }
  }

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
            { value: 'oidc-authorization-code', label: 'OIDC authorization-code (browser login)' },
            { value: 'mtls', label: 'mTLS (client certificate)' },
            { value: 'sigv4', label: 'AWS SigV4' },
            { value: 'hmac', label: 'HMAC signature' },
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
      {auth.type === 'oidc-authorization-code' && (
        <>
          <FormField label="Issuer / discovery URL (auto-fill)">
            <div className="flex gap-2">
              <input
                className={fieldControlClass}
                value={issuer}
                onChange={(e) => setIssuer(e.target.value)}
                placeholder="https://idp.example/realms/myrealm"
                aria-label="Issuer or discovery URL"
              />
              <button
                type="button"
                onClick={discover}
                disabled={discovering || !issuer.trim()}
                className="shrink-0 rounded bg-primary/15 px-2.5 py-1 text-xs font-medium text-primary hover:bg-primary/25 disabled:opacity-50"
              >
                {discovering ? 'Discovering…' : 'Discover'}
              </button>
            </div>
          </FormField>
          <FormField label="Authorization URL">
            <input
              className={fieldControlClass}
              value={auth.auth_url ?? ''}
              onChange={(e) => setAuth({ ...auth, auth_url: e.target.value })}
              placeholder="https://idp/authorize"
              aria-label="Authorization URL"
            />
          </FormField>
          <FormField label="Token URL">
            <input
              className={fieldControlClass}
              value={auth.token_url ?? ''}
              onChange={(e) => setAuth({ ...auth, token_url: e.target.value })}
              aria-label="Token URL (auth-code)"
            />
          </FormField>
          <FormField label="Client ID">
            <input
              className={fieldControlClass}
              value={auth.client_id ?? ''}
              onChange={(e) => setAuth({ ...auth, client_id: e.target.value })}
              aria-label="Client ID (auth-code)"
            />
          </FormField>
          <FormField label="Client secret (optional for PKCE)">
            <input
              className={fieldControlClass}
              type="password"
              value={auth.client_secret ?? ''}
              onChange={(e) => setAuth({ ...auth, client_secret: e.target.value })}
              aria-label="Client secret (auth-code)"
            />
          </FormField>
          <FormField label="Scope">
            <input
              className={fieldControlClass}
              value={auth.scope ?? ''}
              onChange={(e) => setAuth({ ...auth, scope: e.target.value })}
              placeholder="openid profile"
              aria-label="Scope (auth-code)"
            />
          </FormField>
          {redirectURI && (
            <div className="rounded border border-border/60 bg-muted/30 p-2 text-[11px] text-muted-foreground">
              Register this <span className="font-medium">redirect URI</span> in your IdP client:
              <code className="ml-1 break-all text-foreground">{redirectURI}</code>
            </div>
          )}
          <p className="text-[11px] text-muted-foreground">
            After saving, use <span className="font-medium">Log in</span> on the host to open the
            browser sign-in. outwall stores the token — the agent never sees it.
          </p>
        </>
      )}
      {auth.type === 'mtls' && (
        <>
          <FormField label="Client certificate (PEM)">
            <textarea
              className={fieldControlClass}
              rows={3}
              value={auth.client_cert ?? ''}
              onChange={(e) => setAuth({ ...auth, client_cert: e.target.value })}
              aria-label="Client certificate"
            />
          </FormField>
          <FormField label="Client key (PEM)">
            <textarea
              className={fieldControlClass}
              rows={3}
              value={auth.client_key ?? ''}
              onChange={(e) => setAuth({ ...auth, client_key: e.target.value })}
              aria-label="Client key"
            />
          </FormField>
          <FormField label="CA bundle (PEM, optional)">
            <textarea
              className={fieldControlClass}
              rows={2}
              value={auth.ca_bundle ?? ''}
              onChange={(e) => setAuth({ ...auth, ca_bundle: e.target.value })}
              aria-label="CA bundle"
            />
          </FormField>
        </>
      )}
      {auth.type === 'sigv4' && (
        <>
          <FormField label="AWS access key ID">
            <input
              className={fieldControlClass}
              value={auth.aws_access_key_id ?? ''}
              onChange={(e) => setAuth({ ...auth, aws_access_key_id: e.target.value })}
              aria-label="AWS access key ID"
            />
          </FormField>
          <FormField label="AWS secret access key">
            <input
              className={fieldControlClass}
              type="password"
              value={auth.aws_secret_access_key ?? ''}
              onChange={(e) => setAuth({ ...auth, aws_secret_access_key: e.target.value })}
              aria-label="AWS secret access key"
            />
          </FormField>
          <FormField label="Region">
            <input
              className={fieldControlClass}
              value={auth.aws_region ?? ''}
              onChange={(e) => setAuth({ ...auth, aws_region: e.target.value })}
              placeholder="us-east-1"
              aria-label="AWS region"
            />
          </FormField>
          <FormField label="Service">
            <input
              className={fieldControlClass}
              value={auth.aws_service ?? ''}
              onChange={(e) => setAuth({ ...auth, aws_service: e.target.value })}
              placeholder="execute-api"
              aria-label="AWS service"
            />
          </FormField>
        </>
      )}
      {auth.type === 'hmac' && (
        <>
          <FormField label="Secret">
            <input
              className={fieldControlClass}
              type="password"
              value={auth.hmac_secret ?? ''}
              onChange={(e) => setAuth({ ...auth, hmac_secret: e.target.value })}
              aria-label="HMAC secret"
            />
          </FormField>
          <FormField label="Signature header">
            <input
              className={fieldControlClass}
              value={auth.hmac_header ?? ''}
              onChange={(e) => setAuth({ ...auth, hmac_header: e.target.value })}
              placeholder="X-Signature"
              aria-label="HMAC header"
            />
          </FormField>
          <FormField label="Algorithm">
            <Select
              value={auth.hmac_algo ?? 'sha256'}
              onChange={(v) => setAuth({ ...auth, hmac_algo: v })}
              options={[
                { value: 'sha256', label: 'SHA-256' },
                { value: 'sha512', label: 'SHA-512' },
              ]}
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
  const [profile, setProfile] = useState('raw-http')
  const [profiles, setProfiles] = useState<ProfileSchema[]>([])
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

  useEffect(() => {
    getProfiles().then(setProfiles).catch(() => {})
  }, [])

  function openAdd() {
    setName('')
    setBaseURL('')
    setAuth({ type: 'none' })
    setProfile('raw-http')
    setAddOpen(true)
  }

  async function submitAdd(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await createUpstream(name, baseURL, auth, profile)
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
    // Pre-fill with the host's current non-secret auth settings (secrets come back blank); editing
    // one field and leaving secrets blank keeps the stored secret + OIDC tokens (server merges). A
    // host without a credential yet starts on 'static' (a sensible default to set one).
    setCredAuth(u.auth && u.auth.type && u.auth.type !== 'none' ? { ...u.auth } : { type: 'static' })
  }

  async function startLogin(u: Upstream) {
    try {
      const { url, opened } = await oauthLogin(u.name)
      // In the desktop app the daemon already opened the system browser (opened=true); in the
      // plain-browser UI we open it here (the webview would drop window.open).
      if (!opened) window.open(url, '_blank', 'noopener')
      push('success', 'Opened the browser sign-in — complete it, then return here.')
    } catch (err) {
      push('error', err instanceof ApiError ? err.message : 'Failed to start login')
    }
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
              cell: (u) => (
                <div>
                  <span>{u.base_url}</span>
                  {u.browse_url && (
                    <div className="mt-0.5 text-[11px]">
                      <span className="text-muted-foreground">Browse: </span>
                      <a
                        href={u.browse_url}
                        className="font-mono text-primary hover:underline"
                      >
                        {u.browse_url}
                      </a>
                    </div>
                  )}
                </div>
              ),
              className: 'font-mono text-muted-foreground',
            },
            {
              header: 'Credential',
              cell: (u) =>
                !hasCredential(u) ? (
                  <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] font-medium text-muted-foreground">
                    no credential
                  </span>
                ) : u.auth_type === 'oidc-authorization-code' ? (
                  // Configured ≠ logged in: show whether a browser login has actually completed.
                  u.logged_in ? (
                    <span className="rounded bg-success/15 px-1.5 py-0.5 text-[11px] font-medium text-success">
                      logged in (oidc)
                    </span>
                  ) : (
                    <span className="rounded bg-warning/15 px-1.5 py-0.5 text-[11px] font-medium text-warning">
                      not logged in (oidc)
                    </span>
                  )
                ) : (
                  <span className="rounded bg-success/15 px-1.5 py-0.5 text-[11px] font-medium text-success">
                    credential set ({u.auth_type})
                  </span>
                ),
            },
            {
              header: '',
              cell: (u) => (
                <div className="flex justify-end gap-1.5">
                  {u.auth_type === 'oidc-authorization-code' && (
                    <button
                      onClick={() => startLogin(u)}
                      aria-label={`Log in to ${u.name}`}
                      className="rounded bg-success/15 px-2 py-0.5 text-[11px] font-medium text-success hover:bg-success/25"
                    >
                      Log in
                    </button>
                  )}
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
        onClose={() => { setAddOpen(false); setProfile('raw-http') }}
        onSubmit={submitAdd}
        footer={
          <>
            <button
              type="button"
              onClick={() => { setAddOpen(false); setProfile('raw-http') }}
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
        <FormField label="Server type">
          <select
            className={fieldControlClass}
            value={profile}
            onChange={(e) => setProfile(e.target.value)}
          >
            <option value="raw-http">Raw HTTP</option>
            {profiles.map((p) => (
              <option key={p.profile} value={p.profile}>{p.profile}</option>
            ))}
          </select>
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
        {credFor !== null && hasCredential(credFor) && (
          <p className="text-[11px] text-muted-foreground">
            Current settings are pre-filled. Leave secret fields blank to keep the stored value (and
            any OIDC login).
          </p>
        )}
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
