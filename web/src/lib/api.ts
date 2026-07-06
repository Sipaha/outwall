import type {
  VaultStatus,
  Agent,
  Upstream,
  UpstreamAuthConfig,
  ClusterAuthConfig,
  ClusterImportResult,
  Rule,
  ValuePolicy,
  Approval,
  ResolveOptions,
  AccessRequest,
  AuditEntry,
  AuditDetail,
  ProfileSchema,
} from './types'

/** All control-API calls are mounted under /api on the daemon's UIListen bind. */
export const API_BASE = '/api'

/**
 * The single error shape thrown by every helper. Preserves the HTTP status and the daemon's
 * machine-readable error string (the admin handlers emit `{ "error": "..." }` bodies).
 */
export class ApiError extends Error {
  readonly status: number
  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function extractApiError(res: Response): Promise<ApiError> {
  let message = res.statusText || `HTTP ${res.status}`
  try {
    const body = await res.json()
    if (typeof body?.error === 'string') message = body.error
  } catch {
    /* not JSON — fall through with statusText */
  }
  return new ApiError(message, res.status)
}

function fetchWithTimeout(url: string, opts?: RequestInit, timeoutMs = 30_000): Promise<Response> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  if (opts?.signal) {
    opts.signal.addEventListener('abort', () => controller.abort(), { once: true })
  }
  return fetch(url, { ...opts, signal: controller.signal }).finally(() => clearTimeout(timer))
}

type HttpMethod = 'GET' | 'POST' | 'PUT' | 'DELETE'

let sessionRequiredHandler: (() => Promise<boolean>) | null = null

/**
 * Register a callback fired when the daemon rejects a privileged call with 403 "operator session
 * required", so the UI can prompt for the master password. The callback returns a promise that
 * resolves `true` once the operator successfully opens the session, or `false` if they dismiss the
 * prompt without opening one — `request` awaits it to decide whether to retry. Passing null clears
 * it. Concurrent callers should share one pending promise so only one modal ever shows.
 */
export function setSessionRequiredHandler(fn: (() => Promise<boolean>) | null): void {
  sessionRequiredHandler = fn
}

/**
 * Single transport: prefixes API_BASE, sets Content-Type, serializes the body, and converts
 * every non-2xx into an ApiError. When the daemon returns 403 "operator session required" (the
 * operator plane is sealed behind the master-password session — ADR-0041) it fires the registered
 * handler so the UI can prompt, awaits the outcome, and — mirroring the CLI's sudo-style
 * `doPrivileged` (internal/cli/session.go) — retries the call exactly once if the operator opened
 * the session. `isRetry` guards against retrying more than once (a 403 on the retry itself is
 * thrown, not retried again).
 */
async function request<T>(method: HttpMethod, path: string, body?: unknown, isRetry = false): Promise<T> {
  const headers: Record<string, string> = {}
  let payload: BodyInit | undefined
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  const res = await fetchWithTimeout(API_BASE + path, { method, headers, body: payload })
  if (!res.ok) {
    const err = await extractApiError(res)
    if (!isRetry && err.status === 403 && err.message === 'operator session required' && sessionRequiredHandler) {
      const opened = await sessionRequiredHandler()
      if (opened) {
        return request<T>(method, path, body, true)
      }
    }
    throw err
  }
  const text = await res.text()
  return (text ? JSON.parse(text) : undefined) as T
}

// --- Vault ---

export function getVaultStatus(): Promise<VaultStatus> {
  return request('GET', '/vault/status')
}

export function vaultInit(password: string): Promise<{ initialized: boolean }> {
  return request('POST', '/vault/init', { password })
}

export function vaultUnlock(password: string): Promise<{ locked: boolean }> {
  return request('POST', '/vault/unlock', { password })
}

export function vaultLock(): Promise<{ locked: boolean }> {
  return request('POST', '/vault/lock')
}

// --- Operator session (master-password gate; ADR-0041) ---

export interface OperatorSessionStatus {
  open: boolean
  idle_remaining_seconds: number
}

/** Open the operator session by verifying the master password (does NOT unlock the vault). */
export function openOperatorSession(password: string): Promise<OperatorSessionStatus> {
  return request('POST', '/operator/session/open', { password })
}

/** Close the operator session ("Lock now"). The vault stays unlocked; the data plane keeps serving. */
export function lockOperatorSession(): Promise<{ open: boolean }> {
  return request('POST', '/operator/session/lock')
}

/** Current operator-session state (open + idle seconds remaining). Read-only; does not slide the TTL. */
export function getOperatorSessionStatus(): Promise<OperatorSessionStatus> {
  return request('GET', '/operator/session/status')
}

// --- Agents ---

export function listAgents(): Promise<Agent[]> {
  return request('GET', '/agents')
}

/** Delete an agent (privileged) — cascades to remove its policy rules server-side. */
export function deleteAgent(id: string): Promise<{ ok: boolean }> {
  return request('DELETE', `/agents/${encodeURIComponent(id)}`)
}

// --- Upstreams ---

export function listUpstreams(): Promise<Upstream[]> {
  return request('GET', '/upstreams')
}

export function createUpstream(
  name: string,
  baseURL: string,
  auth: UpstreamAuthConfig,
  profile?: string,
): Promise<{ id: string }> {
  return request('POST', '/upstreams', { name, base_url: baseURL, auth, profile })
}

export function deleteUpstream(name: string): Promise<{ ok: boolean }> {
  return request('DELETE', `/upstreams/${encodeURIComponent(name)}`)
}

/**
 * Set (or replace) the credential on an existing host upstream. The Hosts screen uses this to
 * attach a credential after a host was registered lazily (or to rotate it). Secrets are
 * write-only — they never come back on the list response.
 */
export function setUpstreamAuth(name: string, auth: UpstreamAuthConfig): Promise<{ ok: boolean }> {
  return request('POST', `/upstreams/${encodeURIComponent(name)}/auth`, { auth })
}

/**
 * Start a browser OIDC authorization-code login for an upstream. Returns the IdP authorize URL;
 * the caller opens it in a browser. outwall holds the resulting token — the agent never sees it.
 */
export function oauthLogin(name: string): Promise<{ url: string; opened: boolean }> {
  return request('POST', `/upstreams/${encodeURIComponent(name)}/oauth/login`)
}

/** OIDC discovery result (the subset of the well-known document outwall uses). */
export interface OIDCDiscovery {
  issuer: string
  authorization_endpoint: string
  token_endpoint: string
  end_session_endpoint?: string
  scopes_supported?: string[]
}

/** Fetch an OIDC provider's discovery document for an issuer (or full discovery) URL, to auto-fill
 *  the host form's OIDC endpoints. */
export function discoverOIDC(url: string): Promise<OIDCDiscovery> {
  return request('POST', '/oidc/discover', { url })
}

/** The fixed OIDC browser-login redirect URI the operator must register in their IdP. */
export function oidcRedirectURI(): Promise<{ redirect_uri: string }> {
  return request('GET', '/oidc/redirect-uri')
}

// --- Clusters (kind=k8s upstreams) ---

/** Create a kind=k8s cluster. Reuses POST /upstreams with kind:"k8s". */
export function createCluster(
  name: string,
  baseURL: string,
  auth: ClusterAuthConfig,
): Promise<{ id: string }> {
  return request('POST', '/upstreams', { name, base_url: baseURL, kind: 'k8s', auth })
}

/** Import clusters from the host's kubeconfig(s). Returns the names added / already-present. */
export function importClusters(): Promise<ClusterImportResult> {
  return request('POST', '/clusters/import')
}

/**
 * Import clusters from an operator-uploaded kubeconfig (the file-picker path). The raw YAML is
 * the request body — the daemon imports it via ImportContent when the body is non-empty.
 */
export async function importKubeconfigContent(content: string): Promise<ClusterImportResult> {
  const res = await fetchWithTimeout(API_BASE + '/clusters/import', {
    method: 'POST',
    headers: { 'Content-Type': 'application/yaml' },
    body: content,
  })
  if (!res.ok) throw await extractApiError(res)
  const text = await res.text()
  return (text ? JSON.parse(text) : { added: [], updated: [], skipped: [] }) as ClusterImportResult
}

/** Assemble an agent kubeconfig for a cluster (agent token + the local CA). */
export function getKubeconfig(cluster: string, token: string): Promise<{ kubeconfig: string }> {
  return request('POST', '/kubeconfig', { cluster, token })
}

// --- Rules ---

export function listRules(): Promise<Rule[]> {
  return request('GET', '/rules')
}

export function createRule(rule: Omit<Rule, 'id'>): Promise<{ id: string }> {
  return request('POST', '/rules', rule)
}

export function deleteRule(id: string): Promise<{ ok: boolean }> {
  return request('DELETE', `/rules/${encodeURIComponent(id)}`)
}

// --- Profiles ---

export function getProfiles(): Promise<ProfileSchema[]> {
  return request('GET', '/profiles')
}

/**
 * Replace the value policy for a single variable on an existing operation rule. The Operations
 * screen computes the new policy client-side (add a value → set + the value appended; remove → set
 * minus the value; toggle "any" → mode "any") and posts the whole policy — one uniform endpoint for
 * add / remove / trust-any rather than three.
 */
export function setRuleVariablePolicy(
  ruleID: string,
  varName: string,
  policy: ValuePolicy,
): Promise<{ ok: boolean }> {
  return request('POST', `/rules/${encodeURIComponent(ruleID)}/value-policy`, {
    var: varName,
    policy,
  })
}

// --- Approvals ---

export function listApprovals(): Promise<Approval[]> {
  return request('GET', '/approvals')
}

/**
 * Resolve a pending approval. `opts` carries the host credential to attach (host-access cards) and
 * the `trust_any` variable list (operation / new-value cards). Omitting `opts` is a plain
 * approve/deny — the shape the data-plane / k8s cards use.
 */
export function resolveApproval(
  id: string,
  approve: boolean,
  opts?: ResolveOptions,
): Promise<{ ok: boolean }> {
  return request('POST', `/approvals/${encodeURIComponent(id)}/resolve`, { approve, ...opts })
}

// --- Presets ---

export function previewPreset(
  upstream_id: string,
  preset_id: string,
  bindings: Record<string, string>,
): Promise<{ rules: string[] }> {
  return request('POST', '/presets/preview', { upstream_id, preset_id, bindings })
}

// --- Access requests ---

export function listAccessRequests(): Promise<AccessRequest[]> {
  return request('GET', '/access-requests')
}

export function resolveAccessRequest(id: string, status: string): Promise<{ ok: boolean }> {
  return request('POST', `/access-requests/${encodeURIComponent(id)}/resolve`, { status })
}

/** Revoke a granted access request (privileged) — removes the rules it created for the upstream. */
export function revokeAccessRequest(id: string): Promise<{ ok: boolean }> {
  return request('POST', `/access-requests/${encodeURIComponent(id)}/revoke`)
}

// --- Audit ---

export function listAudit(limit = 50): Promise<AuditEntry[]> {
  return request('GET', `/audit?limit=${encodeURIComponent(String(limit))}`)
}

export function getAudit(id: string): Promise<AuditDetail> {
  return request('GET', `/audit/${encodeURIComponent(id)}`)
}

export function pruneAudit(olderThanRFC3339: string): Promise<{ deleted: number }> {
  return request('POST', '/audit/prune', { older_than_rfc3339: olderThanRFC3339 })
}

export function getAuditRetention(): Promise<{ days: number }> {
  return request('GET', '/settings/audit-retention')
}

export function setAuditRetention(days: number): Promise<{ days: number }> {
  return request('PUT', '/settings/audit-retention', { days })
}
