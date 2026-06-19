// Package proxy is the data plane: a localhost reverse proxy that authenticates the
// calling agent, enforces default-deny, injects upstream credentials, and forwards.
package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/audit"
	"github.com/Sipaha/outwall/internal/authn"
	"github.com/Sipaha/outwall/internal/k8s"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/upstream"
)

// Deps are the data-plane dependencies.
type Deps struct {
	Agents      *agent.Registry
	Upstreams   *upstream.Registry
	Policy      *policy.Registry
	Limiter     *policy.Limiter
	Approvals   *approval.Queue
	AuthManager *authn.Manager
	Vault       *secret.Vault
	Audit       *audit.Recorder // optional; nil disables audit (Plans 1–3 behavior).
	Logger      *slog.Logger
}

type handler struct {
	Deps
}

// New builds the data-plane HTTP handler.
func New(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &handler{Deps: d}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Vault.Locked() {
		writeErr(w, http.StatusServiceUnavailable, "vault locked")
		return
	}

	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		writeErr(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	token := strings.TrimPrefix(authz, "Bearer ")
	ag, err := h.Agents.Authenticate(token)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid token")
		return
	}

	// Split "/<upstream>/<rest...>".
	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	name, rest, _ := strings.Cut(trimmed, "/")
	if name == "" {
		writeErr(w, http.StatusNotFound, "no upstream in path")
		return
	}
	up, err := h.Upstreams.GetByName(name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown upstream")
		return
	}

	relPath := "/" + rest

	// escRelPath preserves percent-encoding (e.g. %2F inside one segment) so the operation
	// template matcher splits on real '/' only and an encoded slash stays within a segment.
	// r.URL.EscapedPath() is "/<upstream>/<rest...>"; strip the leading "/<upstream>".
	escRelPath := relPath
	if esc := r.URL.EscapedPath(); strings.HasPrefix(esc, "/"+name) {
		escRelPath = strings.TrimPrefix(esc, "/"+name)
		if escRelPath == "" {
			escRelPath = "/"
		}
	}

	isK8s := up.Kind == upstream.KindK8s
	var ri k8s.RequestInfo
	var pending approval.Pending
	var dec policy.Decision
	// isUpgrade marks an interactive k8s subresource (exec/attach/portforward): an HTTP
	// connection upgrade carrying a duplex stream, not a request/response body. Such requests
	// bypass body capture (it would corrupt the 101 stream) and audit metadata only.
	isUpgrade := false
	if isK8s {
		ri = k8s.Parse(r.Method, relPath, r.URL.Query())
		isUpgrade = ri.IsUpgrade()
		if !ri.IsResource {
			// Discovery / health (/version, /api, /apis, /openapi/...). kubectl needs these
			// to function: allow GET discovery for any agent holding >=1 allow/approval rule
			// on this cluster, else deny.
			ok, derr := h.agentHasAnyGrant(ag.ID, up.ID)
			if derr != nil {
				writeErr(w, http.StatusInternalServerError, "policy error")
				return
			}
			if !ok {
				h.recordOutcome(r, ag, up, relPath, http.StatusForbidden, "deny", nil, "discovery denied (no grant on cluster)")
				writeErr(w, http.StatusForbidden, "access denied")
				return
			}
			dec = policy.Decision{Outcome: policy.Allow}
		} else {
			dec, err = h.Policy.Decide(policy.Input{AgentID: ag.ID, UpstreamID: up.ID, Kind: "k8s",
				Namespace: ri.Namespace, Resource: ri.Resource, Subresource: ri.Subresource, Verb: ri.Verb})
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "policy error")
				return
			}
		}
		pending = approval.Pending{
			AgentID: ag.ID, UpstreamID: up.ID, Method: r.Method, Path: relPath,
			Namespace: ri.Namespace, Resource: resourceKey(ri), Verb: ri.Verb,
		}
	} else {
		// Body-variable gating needs the request body at decision time. For body-bearing methods,
		// read it once up front, pass it to Decide, and restore r.Body so the proxy + audit tee
		// re-read the same bytes (the upstream credential is injected later, never in this body).
		var bodyBytes []byte
		if r.Body != nil && methodMayHaveBody(r.Method) {
			bodyBytes, err = io.ReadAll(r.Body)
			_ = r.Body.Close()
			if err != nil {
				writeErr(w, http.StatusBadRequest, "read request body")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			r.ContentLength = int64(len(bodyBytes))
		}
		dec, err = h.Policy.Decide(policy.Input{AgentID: ag.ID, UpstreamID: up.ID,
			Method: r.Method, Path: escRelPath, Query: r.URL.Query(), Body: bodyBytes})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "policy error")
			return
		}
		pending = approval.Pending{AgentID: ag.ID, UpstreamID: up.ID, Method: r.Method, Path: relPath}
		// On an http new-value approval, carry the matched rule + the not-yet-allowed pairs so
		// the operator card shows "new value" and the resolve path can extend the set.
		if dec.Outcome == policy.RequireApproval && len(dec.NewValues) > 0 && dec.Rule != nil {
			pending.RuleID = dec.Rule.ID
			pending.Template = dec.Rule.OpPathTemplate
			for _, nv := range dec.NewValues {
				pending.NewValues = append(pending.NewValues, approval.NewValue{Var: nv.Var, Value: nv.Value})
			}
		}
	}

	// For a k8s mutating request gated by approval, capture the agent-sent body once and put
	// it on the Pending so the operator sees exactly what will change. The captured bytes
	// replace r.Body so the proxy (and the audit tee) re-read the same payload — no
	// double-read of the underlying stream. The cluster credential is injected later, in the
	// proxy Rewrite step, and so is never part of this captured body.
	if dec.Outcome == policy.RequireApproval && isK8s && isMutatingVerb(ri.Verb) && r.Body != nil {
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "read request body")
			return
		}
		// The forwarded body is the full payload; the approval preview is capped (matching
		// audit) so a huge apply does not bloat the queue.
		pending.RequestBody = capBytes(body, audit.BodyCap)
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}

	switch dec.Outcome {
	case policy.Deny:
		h.recordOutcome(r, ag, up, relPath, http.StatusForbidden, "deny", dec.Rule, "access denied")
		writeErr(w, http.StatusForbidden, "access denied")
		return
	case policy.RequireApproval:
		ok, err := h.Approvals.Submit(r.Context(), pending)
		if err != nil {
			writeErr(w, http.StatusGatewayTimeout, "approval wait canceled")
			return
		}
		if !ok {
			h.recordOutcome(r, ag, up, relPath, http.StatusForbidden, "require-approval", dec.Rule, "request not approved")
			writeErr(w, http.StatusForbidden, "request not approved")
			return
		}
		// An approved http new-value request extends the matched rule's value-sets so the same
		// operation template now admits these values without re-approval.
		if !isK8s && len(dec.NewValues) > 0 && dec.Rule != nil {
			for _, nv := range dec.NewValues {
				if aerr := h.Policy.AddAllowedValue(dec.Rule.ID, nv.Var, nv.Value); aerr != nil {
					h.Logger.Error("extend allowed value", "rule", dec.Rule.ID, "var", nv.Var, "err", aerr)
				}
			}
		}
	}
	if dec.Rule != nil && dec.Rule.RateLimitPerMin > 0 {
		if !h.Limiter.Allow(ag.ID+"|"+dec.Rule.ID, dec.Rule.RateLimitPerMin, time.Now()) {
			h.recordOutcome(r, ag, up, relPath, http.StatusTooManyRequests, dec.Outcome, dec.Rule, "rate limited")
			writeErr(w, http.StatusTooManyRequests, "rate limited")
			return
		}
	}

	base, err := url.Parse(up.BaseURL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "bad upstream url")
		return
	}
	auth, err := h.AuthManager.Authenticator(up)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "auth config error")
		return
	}
	transport, err := h.AuthManager.Transport(up)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "transport config error")
		return
	}

	if isK8s {
		h.Logger.Info("k8s request", "cluster", up.Name, "namespace", ri.Namespace,
			"resource", resourceKey(ri), "verb", ri.Verb, "decision", dec.Outcome)
	}

	// Audit capture state for the proxied request.
	var (
		started        = time.Now()
		ruleID         = ruleIDOf(dec.Rule)
		operation      = operationOf(dec.Rule)
		auditVars      = dec.Vars
		maskedHeaders  map[string]string
		reqCapture     *audit.Capture
		reqContentType string
		auditRecording = h.Audit != nil
		// captureBodies is true only for ordinary request/response audit. Interactive upgrades
		// carry a duplex stream, so body capture is skipped (it would corrupt the 101) and audit
		// records metadata only (see the upgrade branch below).
		captureBodies = auditRecording && !isUpgrade
	)
	if auditRecording {
		maskedHeaders = audit.MaskHeaders(r.Header)
	}
	if captureBodies && r.Body != nil {
		reqContentType = r.Header.Get("Content-Type")
		r.Body, reqCapture = audit.NewCaptureRef(r.Body, audit.BodyCap)
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = base.Scheme
			pr.Out.URL.Host = base.Host
			pr.Out.Host = base.Host
			pr.Out.URL.Path = singleJoin(base.Path, rest)
			pr.Out.URL.RawQuery = r.URL.RawQuery
			pr.Out.Header.Del("Authorization") // never forward the agent's token
			if err := auth.Apply(pr.Out); err != nil {
				h.Logger.Error("apply upstream auth", "upstream", up.Name, "err", err)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			h.Logger.Error("proxy upstream", "upstream", up.Name, "err", err)
			if auditRecording {
				reqBody, reqSize := buildReqBody(reqCapture, reqContentType)
				e := audit.Entry{
					TS: time.Now().UTC(), AgentID: ag.ID, AgentName: ag.Name,
					UpstreamID: up.ID, UpstreamName: up.Name, Method: r.Method,
					Path: relPath, Query: r.URL.RawQuery, StatusCode: http.StatusBadGateway,
					DurationMs: int(time.Since(started).Milliseconds()),
					ReqBytes:   reqSize,
					Decision:   dec.Outcome, RuleID: ruleID, Operation: operation, Vars: auditVars,
					Headers: maskedHeaders,
					Error:   err.Error(),
				}
				if reqBody != nil {
					h.record(e, *reqBody)
				} else {
					h.record(e)
				}
			}
			writeErr(w, http.StatusBadGateway, "upstream error")
		},
	}
	if captureBodies {
		rp.ModifyResponse = func(resp *http.Response) error {
			status := resp.StatusCode
			ctype := resp.Header.Get("Content-Type")
			resp.Body = audit.NewCapture(resp.Body, audit.BodyCap, func(stored []byte, total int64, truncated bool) {
				respBody := audit.ClassifyBody(audit.KindResponse, ctype, stored, total, truncated)
				reqBody, reqSize := buildReqBody(reqCapture, reqContentType)
				bodies := []audit.Body{}
				if reqBody != nil {
					bodies = append(bodies, *reqBody)
				}
				bodies = append(bodies, respBody)
				h.record(audit.Entry{
					TS: time.Now().UTC(), AgentID: ag.ID, AgentName: ag.Name,
					UpstreamID: up.ID, UpstreamName: up.Name, Method: r.Method,
					Path: relPath, Query: r.URL.RawQuery, StatusCode: status,
					DurationMs: int(time.Since(started).Milliseconds()),
					ReqBytes:   reqSize, RespBytes: total,
					Decision: dec.Outcome, RuleID: ruleID, Operation: operation, Vars: auditVars,
					Headers: maskedHeaders,
				}, bodies...)
			})
			return nil
		}
	}
	if transport != nil {
		rp.Transport = transport
	}
	if isK8s {
		// Stream watch/log responses live: flush each chunk immediately so `logs -f` / `-w`
		// reach the agent incrementally rather than being buffered.
		rp.FlushInterval = -1
	}

	// For an interactive upgrade with audit on, wrap the ResponseWriter so the hijacked client
	// connection counts the bytes streamed each way and emits a metadata audit record when the
	// session closes. Body capture is already bypassed above (the duplex stream has no
	// request/response bodies); this is the only audit for an exec/attach/portforward session.
	if isUpgrade && auditRecording {
		w = &hijackAuditWriter{ResponseWriter: w, onClose: func(in, out int64, status int) {
			h.record(audit.Entry{
				TS: time.Now().UTC(), AgentID: ag.ID, AgentName: ag.Name,
				UpstreamID: up.ID, UpstreamName: up.Name, Method: r.Method,
				Path: relPath, Query: r.URL.RawQuery, StatusCode: status,
				DurationMs: int(time.Since(started).Milliseconds()),
				ReqBytes:   in, RespBytes: out,
				Decision: dec.Outcome, RuleID: ruleID, Headers: maskedHeaders,
			})
		}}
	}
	rp.ServeHTTP(w, r)
}

// recordOutcome records a non-proxied early policy outcome (deny / approval-denied / rate-limited).
func (h *handler) recordOutcome(r *http.Request, ag *agent.Agent, up *upstream.Upstream, relPath string, status int, decision string, rule *policy.Rule, errMsg string) {
	if h.Audit == nil {
		return
	}
	h.record(audit.Entry{
		TS: time.Now().UTC(), AgentID: ag.ID, AgentName: ag.Name,
		UpstreamID: up.ID, UpstreamName: up.Name, Method: r.Method,
		Path: relPath, Query: r.URL.RawQuery, StatusCode: status,
		Decision: decision, RuleID: ruleIDOf(rule),
		Headers: audit.MaskHeaders(r.Header), Error: errMsg,
	})
}

func (h *handler) record(e audit.Entry, bodies ...audit.Body) {
	if err := h.Audit.Record(e, bodies...); err != nil {
		h.Logger.Error("record audit entry", "err", err)
	}
}

// buildReqBody classifies the captured request body, or returns nil when no body was sent.
func buildReqBody(cap *audit.Capture, contentType string) (*audit.Body, int64) {
	if cap == nil {
		return nil, 0
	}
	stored, total, truncated := cap.Captured()
	if total == 0 {
		return nil, 0
	}
	b := audit.ClassifyBody(audit.KindRequest, contentType, stored, total, truncated)
	return &b, total
}

// agentHasAnyGrant reports whether the agent holds at least one allow/require-approval rule
// on the given cluster (its own rules or any-subject rules), and no agent-specific deny that
// would shut it out. Used to gate k8s discovery/health endpoints kubectl needs to function.
func (h *handler) agentHasAnyGrant(agentID, upstreamID string) (bool, error) {
	rules, err := h.Policy.ForUpstream(upstreamID)
	if err != nil {
		return false, err
	}
	hasGrant := false
	for _, rule := range rules {
		if rule.SubjectAgentID != "" && rule.SubjectAgentID != agentID {
			continue
		}
		if rule.SubjectAgentID == agentID && rule.Outcome == policy.Deny {
			return false, nil
		}
		if rule.Outcome == policy.Allow || rule.Outcome == policy.RequireApproval {
			hasGrant = true
		}
	}
	return hasGrant, nil
}

// resourceKey renders the resource (with subresource, if any) for display/audit, e.g.
// "pods" or "pods/log".
func resourceKey(ri k8s.RequestInfo) string {
	if ri.Subresource != "" {
		return ri.Resource + "/" + ri.Subresource
	}
	return ri.Resource
}

// methodMayHaveBody reports whether an HTTP method typically carries a request body that may hold
// gated body variables. GET/HEAD never do, so their body (if any) is not pre-read.
func methodMayHaveBody(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// isMutatingVerb reports whether a k8s RBAC verb changes cluster state and so should be
// gated by approval with its body shown to the operator.
func isMutatingVerb(verb string) bool {
	switch verb {
	case "create", "update", "patch", "delete", "deletecollection":
		return true
	default:
		return false
	}
}

// capBytes returns b truncated to at most cap bytes (a copy, so the preview never aliases
// the larger forwarded buffer).
func capBytes(b []byte, cap int) []byte {
	if len(b) > cap {
		b = b[:cap]
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func ruleIDOf(rule *policy.Rule) string {
	if rule == nil {
		return ""
	}
	return rule.ID
}

// operationOf returns the matched rule's operation path-template, for the audit record (§8).
func operationOf(rule *policy.Rule) string {
	if rule == nil {
		return ""
	}
	return rule.OpPathTemplate
}

func singleJoin(a, b string) string {
	a = strings.TrimSuffix(a, "/")
	b = strings.TrimPrefix(b, "/")
	if b == "" {
		if a == "" {
			return "/"
		}
		return a
	}
	return a + "/" + b
}
