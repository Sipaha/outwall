package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/audit"
	"github.com/Sipaha/outwall/internal/k8s"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/upstream"
)

// apiMux registers the shared admin API routes plus the SSE event stream onto a fresh mux.
// Both transports — the CSRF-free unix socket (AdminHandler, for the local CLI) and the
// CSRF-gated TCP listener (UIHandler, for the desktop UI) — build their handler from this
// same route table.
func (d *Daemon) apiMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /vault/init", d.hVaultInit)
	mux.HandleFunc("POST /vault/unlock", d.hVaultUnlock)
	mux.HandleFunc("POST /vault/lock", d.hVaultLock)
	mux.HandleFunc("GET /vault/status", d.hVaultStatus)
	mux.HandleFunc("POST /upstreams", d.hUpstreamCreate)
	mux.HandleFunc("GET /upstreams", d.hUpstreamList)
	mux.HandleFunc("DELETE /upstreams/{name}", d.hUpstreamDelete)
	mux.HandleFunc("POST /agents/register", d.hAgentRegister)
	mux.HandleFunc("GET /agents", d.hAgentList)
	mux.HandleFunc("POST /kubeconfig", d.hKubeconfig)
	mux.HandleFunc("POST /rules", d.hRuleCreate)
	mux.HandleFunc("GET /rules", d.hRuleList)
	mux.HandleFunc("DELETE /rules/{id}", d.hRuleDelete)
	mux.HandleFunc("GET /approvals", d.hApprovalList)
	mux.HandleFunc("POST /approvals/{id}/resolve", d.hApprovalResolve)
	mux.HandleFunc("GET /access-requests", d.hAccessRequestList)
	mux.HandleFunc("POST /access-requests/{id}/resolve", d.hAccessRequestResolve)
	mux.HandleFunc("GET /audit", d.hAuditList)
	mux.HandleFunc("GET /audit/{id}", d.hAuditGet)
	mux.HandleFunc("POST /audit/prune", d.hAuditPrune)
	mux.HandleFunc("GET /events", sseHandler(d.bus))
	return mux
}

// AdminHandler builds the admin API mux served over the unix socket (CSRF-free, local CLI).
func (d *Daemon) AdminHandler() http.Handler { return d.apiMux() }

// UIHandler builds the desktop-UI handler served over the UIListen TCP bind: the embedded
// SPA at "/" and the shared admin/SSE mux under "/api" behind the X-Outwall-CSRF gate. The
// CSRF header is a CSRF-not-auth boundary — loopback bind + single-tenant host is the trust
// model (ADR-0005). Static assets are not CSRF-gated; only "/api/**" is.
func (d *Daemon) UIHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", csrfMiddleware(d.apiMux())))
	mux.Handle("/", staticUI())
	return mux
}

// csrfMiddleware rejects any request lacking a non-empty X-Outwall-CSRF header with 403. It
// defeats browser cross-origin form posts; it is NOT authentication (see ADR-0005).
//
// GET /events (SSE) is exempt: EventSource cannot set custom request headers, so the events
// stream could never carry X-Outwall-CSRF. This is safe — SSE is read-only (it changes no
// state), same-origin, and served only over the loopback UIListen bind (see ADR-0005).
func csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/events" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("X-Outwall-CSRF") == "" {
			adminErr(w, http.StatusForbidden, "missing csrf header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func adminErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }

func (d *Daemon) hVaultInit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := d.vault.Init(body.Password); err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"initialized": true})
}

func (d *Daemon) hVaultUnlock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	switch err := d.vault.Unlock(body.Password); {
	case err == nil:
		d.publish("vault.unlocked", map[string]any{})
		writeJSON(w, http.StatusOK, map[string]bool{"locked": false})
	case errors.Is(err, secret.ErrBadPassword):
		adminErr(w, http.StatusUnauthorized, "incorrect master password")
	default:
		adminErr(w, http.StatusBadRequest, err.Error())
	}
}

func (d *Daemon) hVaultLock(w http.ResponseWriter, _ *http.Request) {
	d.vault.Lock()
	d.publish("vault.locked", map[string]any{})
	writeJSON(w, http.StatusOK, map[string]bool{"locked": true})
}

func (d *Daemon) hVaultStatus(w http.ResponseWriter, _ *http.Request) {
	init, err := d.vault.Initialized()
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"initialized": init, "locked": d.vault.Locked()})
}

func (d *Daemon) hUpstreamCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string              `json:"name"`
		BaseURL string              `json:"base_url"`
		Kind    string              `json:"kind"`
		Auth    upstream.AuthConfig `json:"auth"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	up, err := d.upstreams.CreateKind(body.Name, body.BaseURL, body.Kind, body.Auth)
	if err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d.publish("upstream.created", map[string]any{"id": up.ID, "name": up.Name, "kind": up.Kind})
	writeJSON(w, http.StatusOK, map[string]string{"id": up.ID})
}

func (d *Daemon) hUpstreamDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := d.upstreams.DeleteByName(name); err != nil {
		if errors.Is(err, upstream.ErrNotFound) {
			adminErr(w, http.StatusNotFound, "unknown upstream")
			return
		}
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.publish("upstream.deleted", map[string]any{"name": name})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// hKubeconfig assembles an agent kubeconfig for a cluster from an agent token + the local CA.
func (d *Daemon) hKubeconfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cluster string `json:"cluster"`
		Token   string `json:"token"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if _, err := d.agents.Authenticate(body.Token); err != nil {
		adminErr(w, http.StatusBadRequest, "unknown agent token")
		return
	}
	cl, err := d.upstreams.GetByName(body.Cluster)
	if err != nil {
		adminErr(w, http.StatusNotFound, "unknown cluster")
		return
	}
	if cl.Kind != upstream.KindK8s {
		adminErr(w, http.StatusBadRequest, "not a k8s cluster")
		return
	}
	serverURL := d.DataPlaneURL() + "/" + cl.Name
	yamlBytes, err := k8s.Kubeconfig(serverURL, cl.Name, string(d.CAPEM()), body.Token)
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"kubeconfig": string(yamlBytes)})
}

func (d *Daemon) hUpstreamList(w http.ResponseWriter, _ *http.Request) {
	ups, err := d.upstreams.List()
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]string, 0, len(ups))
	for _, u := range ups {
		out = append(out, map[string]string{
			"id": u.ID, "name": u.Name, "base_url": u.BaseURL, "auth_type": u.AuthType, "kind": u.Kind,
		}) // secrets intentionally omitted
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *Daemon) hAgentRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	a, token, err := d.agents.Register(body.Name)
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.publish("agent.registered", map[string]any{"id": a.ID, "name": a.Name})
	writeJSON(w, http.StatusOK, map[string]string{"id": a.ID, "token": token})
}

func (d *Daemon) hAgentList(w http.ResponseWriter, _ *http.Request) {
	ags, err := d.agents.List()
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]string, 0, len(ags))
	for _, a := range ags {
		out = append(out, map[string]string{"id": a.ID, "name": a.Name, "status": a.Status})
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *Daemon) hRuleCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SubjectAgentID  string `json:"subject_agent_id"`
		UpstreamID      string `json:"upstream_id"`
		Method          string `json:"method"`
		PathGlob        string `json:"path_glob"`
		Outcome         string `json:"outcome"`
		RateLimitPerMin int    `json:"rate_limit_per_min"`
		Namespace       string `json:"namespace"`
		Resource        string `json:"resource"`
		Verb            string `json:"verb"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	rule, err := d.policy.Create(policy.Rule{
		SubjectAgentID: body.SubjectAgentID, UpstreamID: body.UpstreamID, Method: body.Method,
		PathGlob: body.PathGlob, Outcome: body.Outcome, RateLimitPerMin: body.RateLimitPerMin,
		Namespace: body.Namespace, Resource: body.Resource, Verb: body.Verb,
	})
	if err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d.publish("rule.created", map[string]any{"id": rule.ID})
	writeJSON(w, http.StatusOK, map[string]string{"id": rule.ID})
}

func (d *Daemon) hRuleList(w http.ResponseWriter, _ *http.Request) {
	rules, err := d.policy.List()
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		out = append(out, map[string]any{
			"id": rule.ID, "subject_agent_id": rule.SubjectAgentID, "upstream_id": rule.UpstreamID,
			"method": rule.Method, "path_glob": rule.PathGlob, "outcome": rule.Outcome,
			"rate_limit_per_min": rule.RateLimitPerMin,
			"namespace":          rule.Namespace, "resource": rule.Resource, "verb": rule.Verb,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *Daemon) hRuleDelete(w http.ResponseWriter, r *http.Request) {
	if err := d.policy.Delete(r.PathValue("id")); err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (d *Daemon) hApprovalList(w http.ResponseWriter, _ *http.Request) {
	pending := d.approvals.List()
	out := make([]map[string]string, 0, len(pending))
	for _, p := range pending {
		out = append(out, map[string]string{
			"id": p.ID, "agent_id": p.AgentID, "upstream_id": p.UpstreamID,
			"method": p.Method, "path": p.Path, "purpose": p.Purpose,
			"created_at": p.CreatedAt.Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *Daemon) hApprovalResolve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Approve bool `json:"approve"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	switch err := d.approvals.Resolve(r.PathValue("id"), body.Approve); {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, approval.ErrNotFound):
		adminErr(w, http.StatusNotFound, "approval not found")
	default:
		adminErr(w, http.StatusInternalServerError, err.Error())
	}
}

func (d *Daemon) hAccessRequestList(w http.ResponseWriter, _ *http.Request) {
	reqs, err := d.access.List()
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	agentNames := map[string]string{}
	if ags, err := d.agents.List(); err == nil {
		for _, a := range ags {
			agentNames[a.ID] = a.Name
		}
	}
	upstreamNames := map[string]string{}
	if ups, err := d.upstreams.List(); err == nil {
		for _, u := range ups {
			upstreamNames[u.ID] = u.Name
		}
	}
	out := make([]map[string]string, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, map[string]string{
			"id": req.ID, "agent_id": req.AgentID, "agent_name": agentNames[req.AgentID],
			"upstream_id": req.UpstreamID, "upstream_name": upstreamNames[req.UpstreamID],
			"purpose": req.Purpose, "status": req.Status,
			"created_at": req.CreatedAt.Format(time.RFC3339Nano), "resolved_at": req.ResolvedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *Daemon) hAccessRequestResolve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	switch err := d.access.Resolve(r.PathValue("id"), body.Status); {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, access.ErrNotFound):
		adminErr(w, http.StatusNotFound, "access request not found")
	default:
		adminErr(w, http.StatusBadRequest, err.Error())
	}
}

func auditEntryMap(e audit.Entry) map[string]any {
	return map[string]any{
		"id": e.ID, "ts": e.TS.Format(time.RFC3339Nano),
		"agent_id": e.AgentID, "agent_name": e.AgentName,
		"upstream_id": e.UpstreamID, "upstream_name": e.UpstreamName,
		"method": e.Method, "path": e.Path, "query": e.Query,
		"status_code": e.StatusCode, "duration_ms": e.DurationMs,
		"req_bytes": e.ReqBytes, "resp_bytes": e.RespBytes,
		"decision": e.Decision, "rule_id": e.RuleID, "error": e.Error,
	}
}

func (d *Daemon) hAuditList(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}
	entries, err := d.audit.List(limit)
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, auditEntryMap(e))
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *Daemon) hAuditGet(w http.ResponseWriter, r *http.Request) {
	e, bodies, err := d.audit.Get(r.PathValue("id"))
	switch {
	case err == nil:
	case errors.Is(err, audit.ErrNotFound):
		adminErr(w, http.StatusNotFound, "audit entry not found")
		return
	default:
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := auditEntryMap(e)
	out["headers"] = e.Headers
	bodyOut := make([]map[string]any, 0, len(bodies))
	for _, b := range bodies {
		m := map[string]any{
			"kind": b.Kind, "content_type": b.ContentType, "size": b.Size,
			"sha256": b.Sha256, "truncated": b.Truncated,
		}
		if b.Stored != nil {
			m["body"] = string(b.Stored)
		}
		bodyOut = append(bodyOut, m)
	}
	out["bodies"] = bodyOut
	writeJSON(w, http.StatusOK, out)
}

func (d *Daemon) hAuditPrune(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OlderThanRFC3339 string `json:"older_than_rfc3339"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	cutoff, err := time.Parse(time.RFC3339, body.OlderThanRFC3339)
	if err != nil {
		adminErr(w, http.StatusBadRequest, "bad older_than_rfc3339: "+err.Error())
		return
	}
	n, err := d.audit.Prune(cutoff)
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": n})
}
