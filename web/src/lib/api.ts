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
} from './types'

/** All control-API calls are mounted under /api on the daemon's UIListen bind. */
export const API_BASE = '/api'

/**
 * X-Outwall-CSRF is a CSRF-not-auth boundary: the daemon rejects any /api request lacking
 * this header with 403 (see ADR-0005). It defeats browser cross-origin form posts; it is not
 * authentication. GET /api/events (SSE) is exempt because EventSource cannot set headers.
 */
const CSRF_HEADER = { 'X-Outwall-CSRF': '1' }

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

type HttpMethod = 'GET' | 'POST' | 'DELETE'

/**
 * Single transport: prefixes API_BASE, attaches the CSRF header and Content-Type, serializes
 * the body, and converts every non-2xx response into an ApiError. Returns parsed JSON (or
 * undefined for empty bodies).
 */
async function request<T>(method: HttpMethod, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { ...CSRF_HEADER }
  let payload: BodyInit | undefined
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  const res = await fetchWithTimeout(API_BASE + path, { method, headers, body: payload })
  if (!res.ok) throw await extractApiError(res)
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

// --- Agents ---

export function listAgents(): Promise<Agent[]> {
  return request('GET', '/agents')
}

// --- Upstreams ---

export function listUpstreams(): Promise<Upstream[]> {
  return request('GET', '/upstreams')
}

export function createUpstream(
  name: string,
  baseURL: string,
  auth: UpstreamAuthConfig,
): Promise<{ id: string }> {
  return request('POST', '/upstreams', { name, base_url: baseURL, auth })
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
    headers: { ...CSRF_HEADER, 'Content-Type': 'application/yaml' },
    body: content,
  })
  if (!res.ok) throw await extractApiError(res)
  const text = await res.text()
  return (text ? JSON.parse(text) : { added: [], skipped: [] }) as ClusterImportResult
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

// --- Access requests ---

export function listAccessRequests(): Promise<AccessRequest[]> {
  return request('GET', '/access-requests')
}

export function resolveAccessRequest(id: string, status: string): Promise<{ ok: boolean }> {
  return request('POST', `/access-requests/${encodeURIComponent(id)}/resolve`, { status })
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
