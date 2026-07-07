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
  created_at: string // RFC3339Nano
  last_seen_at: string // RFC3339Nano, or "" if the agent has never authenticated
}

/** Upstream auth config (secrets omitted on list responses; used on create). */
export interface UpstreamAuthConfig {
  type: string // none | static | basic | oidc-client-credentials | mtls | sigv4 | hmac
  header?: string
  token?: string
  username?: string
  password?: string
  token_url?: string
  client_id?: string
  client_secret?: string
  scope?: string
  // OIDC authorization-code (browser login). Tokens are server-managed (never sent from the form).
  auth_url?: string
  redirect_url?: string
  // mTLS (client certificate presented to the upstream):
  client_cert?: string
  client_key?: string
  ca_bundle?: string
  // AWS Signature V4:
  aws_access_key_id?: string
  aws_secret_access_key?: string
  aws_region?: string
  aws_service?: string
  // HMAC request signature:
  hmac_secret?: string
  hmac_header?: string
  hmac_algo?: string // sha256 (default) | sha512
}

/** GET /api/upstreams — secrets intentionally omitted by the daemon. */
export interface Upstream {
  id: string
  name: string
  base_url: string
  auth_type: string
  kind?: string // "" / "http" = http upstream; "k8s" = a Kubernetes cluster
  // k8s clusters: the cluster auth method and whether TLS verification is disabled (insecure).
  k8s_auth?: string // token | client-cert | exec
  k8s_insecure?: boolean // true → registered with insecure-skip-tls-verify (drives the red badge)
  // http upstreams: the current non-secret auth settings (secrets cleared) so the replace-credential
  // form can pre-fill. Absent for k8s clusters.
  auth?: UpstreamAuthConfig
  // oidc-authorization-code hosts: true once a browser login completed (tokens held) — distinct from
  // merely being configured. Drives the "logged in" vs "needs login" badge.
  logged_in?: boolean
  profile?: string
  // http upstreams: the local reverse-proxy URL the agent uses to reach this upstream.
  browse_url?: string
  presets?: Preset[]
}

/** Cluster auth config sent on POST /api/upstreams when creating a kind=k8s cluster. */
export interface ClusterAuthConfig {
  type: 'none'
  k8s_auth: 'token' | 'client-cert' | 'exec'
  ca_bundle?: string
  token?: string
  client_cert?: string
  client_key?: string
  exec_command?: string
  exec_args?: string[]
}

/** POST /api/clusters/import response — cluster names added / refreshed-in-place / already-present. */
export interface ClusterImportResult {
  added: string[]
  updated: string[]
  skipped: string[]
}

/** Per-variable value policy on an http operation rule. */
export interface ValuePolicy {
  type: string // "text" | "date" | "number" | "enum"
  mode: string // "set" | "any" | "range"
  values?: string[] // allowed values (text/set, enum/set)
  min?: number // number/range lower bound (inclusive)
  max?: number // number/range upper bound (inclusive)
}

/**
 * GET /api/rules — policy rules. For http upstreams the rule is an operation rule
 * (op_method + op_path_template + op_query_template + per-variable op_value_policies); for k8s
 * clusters it matches on the RBAC tuple namespace/resource/verb.
 */
export interface Rule {
  id: string
  subject_agent_id: string // "" = any agent
  upstream_id: string
  op_method?: string // http method, e.g. "GET" (http rules)
  op_path_template?: string // http operation path-template (http rules)
  op_query_template?: Record<string, string> // query param -> literal or "{name:type}"
  op_body_template?: Record<string, string> // JSON dotted path -> literal or "{name:type}" (body vars)
  op_value_policies?: Record<string, ValuePolicy> // varName -> value policy
  outcome: string // allow | deny | require-approval
  rate_limit_per_min: number
  namespace?: string // k8s: "", "prod", "prod-*", "*"
  resource?: string // k8s: "pods", "pods/log", "deployments", "*"
  verb?: string // k8s: get/list/watch/create/update/patch/delete/deletecollection/*
  profile?: string
  profile_params?: Record<string, unknown>
  browse_methods?: string // http methods allowed for browse access (e.g. "GET,HEAD")
  browse_path?: string // path glob for browse access (e.g. "/**")
  expires_at?: string // RFC3339Nano or '' (never)
  ttl_seconds?: number // write-only: operator's chosen grant duration on create (0 = never expires)
}

export interface ProfileField { key: string; label: string; type: string; options?: string[] }
export interface ProfileSchema { profile: string; fields: ProfileField[] }

export interface PresetSlot {
  key: string
  label: string
  type: string // "text" | "enum"
  options?: string[]
  allow_any: boolean
  required: boolean
}
export interface Preset {
  id: string
  label: string
  slots: PresetSlot[]
}

/** A declared typed operation variable on a KindOperation approval (mirrors approval.Variable). */
export interface OpVariable {
  name: string
  type: string // "text" | "date"
}

/** A not-yet-allowed (variable, value) pair on a data-plane new-value approval. */
export interface NewValue {
  var: string
  value: string
}

/**
 * GET /api/approvals — in-flight require-approval requests blocking the data plane, plus the MCP
 * control-plane host/operation cards. `kind` discriminates:
 *  - "" (empty) → a data-plane new-value approval (has `new_values` + `template`) or a k8s tuple
 *    approval (has namespace/resource/verb).
 *  - "host-access" → a tier-1 host card (agent + host + purpose).
 *  - "operation"   → a tier-2 operation card (the operation shape + declared variables + values).
 *  - "k8s-access"  → an MCP k8s-access card (cluster + namespace/resource/verb + purpose).
 */
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
  k8s_grants?: { namespace: string; resource: string; verb: string }[] // k8s-access card: all requested tuples
  request_body?: string // agent-sent patch/apply body, credentials masked
  // MCP control-plane context (empty for data-plane / k8s approvals).
  kind?: string // "host-access" | "operation" | "k8s-access" | "" (data-plane / k8s)
  host?: string // the host this approval concerns (host-access / operation / k8s-access)
  // Operation card (kind === "operation"):
  op_method?: string
  op_path_template?: string
  op_query_template?: Record<string, string>
  op_variables?: OpVariable[]
  op_values?: Record<string, string> // varName -> the concrete value the agent intends to use
  // Data-plane new-value card (kind === ""):
  new_values?: NewValue[]
  template?: string // matched operation path-template for display
  rule_id?: string
  // Preset card (kind === "preset"):
  preset_id?: string
  bindings?: Record<string, string>
  preset?: Preset
}

/**
 * Options for POST /api/approvals/{id}/resolve. `auth` attaches a host credential when approving a
 * host-access card; `trust_any` lists the operation variables flipped to "trust any value";
 * `bindings` carries the final slot values when approving a preset card.
 */
export interface ResolveOptions {
  auth?: UpstreamAuthConfig
  trust_any?: string[]
  reason?: string // operator's explanation on deny, surfaced to the agent
  bindings?: Record<string, string>
  ttl_seconds?: number // grant duration in seconds (0 = never); rides the approve payload
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
  reason?: string // operator's deny reason (when denied)
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
