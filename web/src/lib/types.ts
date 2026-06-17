// TypeScript shapes mirroring the Go admin-API JSON (see internal/daemon/admin.go and the
// internal/* registries). Field names are the exact JSON keys the daemon emits/accepts.

/** GET /api/vault/status — { initialized, locked }. */
export interface VaultStatus {
  initialized: boolean
  locked: boolean
}

/** GET /api/agents — agent registry rows (token never leaves the daemon). */
export interface Agent {
  id: string
  name: string
  status: string // "new" (agent.StatusNew) by default — default-deny
}

/** Upstream auth config (secrets omitted on list responses; used on create). */
export interface UpstreamAuthConfig {
  type: string // none | static | basic | oidc-client-credentials
  header?: string
  token?: string
  username?: string
  password?: string
  token_url?: string
  client_id?: string
  client_secret?: string
  scope?: string
}

/** GET /api/upstreams — secrets intentionally omitted by the daemon. */
export interface Upstream {
  id: string
  name: string
  base_url: string
  auth_type: string
  kind?: string // "" / "http" = http upstream; "k8s" = a Kubernetes cluster
}

/**
 * GET /api/rules — policy rules. For http upstreams the rule matches on method+path_glob;
 * for k8s clusters it matches on the RBAC tuple namespace/resource/verb.
 */
export interface Rule {
  id: string
  subject_agent_id: string // "" = any agent
  upstream_id: string
  method: string // "" or "*" = any method (http)
  path_glob: string // (http)
  outcome: string // allow | deny | require-approval
  rate_limit_per_min: number
  namespace?: string // k8s: "", "prod", "prod-*", "*"
  resource?: string // k8s: "pods", "pods/log", "deployments", "*"
  verb?: string // k8s: get/list/watch/create/update/patch/delete/deletecollection/*
}

/** GET /api/approvals — in-flight require-approval requests blocking the data plane. */
export interface Approval {
  id: string
  agent_id: string
  upstream_id: string
  method: string
  path: string
  purpose: string
  created_at: string // RFC3339Nano
  namespace?: string // k8s tuple (empty for http approvals)
  resource?: string
  verb?: string
  request_body?: string // agent-sent patch/apply body, credentials masked
}

/** GET /api/access-requests — logged access-request intents. */
export interface AccessRequest {
  id: string
  agent_id: string
  agent_name: string
  upstream_id: string
  upstream_name: string
  purpose: string
  status: string // pending | granted | denied | dismissed
  created_at: string
  resolved_at: string
}

/** GET /api/audit — journal row (no bodies). */
export interface AuditEntry {
  id: string
  ts: string
  agent_id: string
  agent_name: string
  upstream_id: string
  upstream_name: string
  method: string
  path: string
  query: string
  status_code: number
  duration_ms: number
  req_bytes: number
  resp_bytes: number
  decision: string
  rule_id: string
  error: string
}

/** A captured request/response body (returned by GET /api/audit/{id}). */
export interface AuditBody {
  kind: string // request | response
  content_type: string
  size: number
  sha256: string
  truncated: boolean
  body?: string
}

/** GET /api/audit/{id} — full entry with headers + bodies. */
export interface AuditDetail extends AuditEntry {
  headers: Record<string, string>
  bodies: AuditBody[]
}

/** An SSE domain event (event: <type>, data: <json>). See internal/events. */
export interface OutwallEvent {
  type: string
  data: unknown
}
