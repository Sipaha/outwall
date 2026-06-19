package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/audit"
	"github.com/Sipaha/outwall/internal/k8s"
	"github.com/Sipaha/outwall/internal/optemplate"
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
	mux.HandleFunc("POST /upstreams/{name}/auth", d.hUpstreamSetAuth)
	mux.HandleFunc("POST /agents/register", d.hAgentRegister)
	mux.HandleFunc("GET /agents", d.hAgentList)
	mux.HandleFunc("POST /clusters/import", d.hClustersImport)
	mux.HandleFunc("POST /kubeconfig", d.hKubeconfig)
	mux.HandleFunc("POST /rules", d.hRuleCreate)
	mux.HandleFunc("GET /rules", d.hRuleList)
	mux.HandleFunc("DELETE /rules/{id}", d.hRuleDelete)
	mux.HandleFunc("POST /rules/{id}/value-policy", d.hRuleSetVariablePolicy)
	mux.HandleFunc("GET /approvals", d.hApprovalList)
	mux.HandleFunc("POST /approvals/{id}/resolve", d.hApprovalResolve)
	mux.HandleFunc("GET /access-requests", d.hAccessRequestList)
	mux.HandleFunc("POST /access-requests/{id}/resolve", d.hAccessRequestResolve)
	mux.HandleFunc("GET /audit", d.hAuditList)
	mux.HandleFunc("GET /audit/{id}", d.hAuditGet)
	mux.HandleFunc("POST /audit/prune", d.hAuditPrune)
	mux.HandleFunc("GET /settings/audit-retention", d.hAuditRetentionGet)
	mux.HandleFunc("PUT /settings/audit-retention", d.hAuditRetentionSet)
	mux.HandleFunc("GET /events", sseHandler(d.bus))
	mux.HandleFunc("POST /desktop/focus", d.hDesktopFocus)
	return mux
}

// hDesktopFocus raises the desktop window for the single-instance gate (ADR-0013): a second
// launch posts here over the CSRF-free unix socket so the running instance comes to the
// foreground. If no window is registered (OnFocusRequest is nil, e.g. a headless serve) it
// answers 503 so the second launcher can tell "lock held but nobody to focus" from success.
func (d *Daemon) hDesktopFocus(w http.ResponseWriter, _ *http.Request) {
	if d.cfg.OnFocusRequest == nil {
		adminErr(w, http.StatusServiceUnavailable, "no window to focus")
		return
	}
	d.cfg.OnFocusRequest()
	w.WriteHeader(http.StatusNoContent)
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
		// Best-effort auto-import of the host's kubeconfig clusters now that the vault can
		// encrypt their auth. A failure here must NEVER fail the unlock — it is logged only.
		d.autoImportClusters()
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

// hUpstreamSetAuth sets (or replaces) the credential on an existing host upstream — the Hosts
// screen's "set / replace credential" action (a host registered lazily, or a token rotation). The
// secret is encrypted server-side and never returned on the list; this is a write-only path.
func (d *Daemon) hUpstreamSetAuth(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Auth upstream.AuthConfig `json:"auth"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	up, err := d.upstreams.GetByName(name)
	if err != nil {
		if errors.Is(err, upstream.ErrNotFound) {
			adminErr(w, http.StatusNotFound, "unknown upstream")
			return
		}
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := d.upstreams.SetAuth(up.ID, body.Auth); err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d.publish("upstream.updated", map[string]any{"id": up.ID, "name": up.Name})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// maxImportBody caps an uploaded kubeconfig at 1 MiB — far above any real kubeconfig, small
// enough to reject a runaway upload.
const maxImportBody = 1 << 20

// hClustersImport imports clusters. When the request carries a body it is treated as an uploaded
// kubeconfig (the file-picker path) and imported via ImportContent; otherwise it auto-discovers
// and scans the host's kubeconfig files. Either way it returns the (non-nil) names added/skipped.
// It requires the vault unlocked (Create encrypts).
func (d *Daemon) hClustersImport(w http.ResponseWriter, r *http.Request) {
	body, readErr := io.ReadAll(io.LimitReader(r.Body, maxImportBody))
	if readErr != nil {
		adminErr(w, http.StatusBadRequest, "read request body")
		return
	}

	var added, skipped []string
	var err error
	if len(body) > 0 {
		// Operator-uploaded kubeconfig: no baseDir (managed configs use inline *-data).
		added, skipped, err = d.importer.ImportContent(body, "")
	} else {
		added, skipped, err = d.importer.Import(k8s.DiscoverKubeconfigPaths())
	}
	if err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(added) > 0 {
		d.publish("upstream.created", map[string]any{"count": len(added)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "skipped": skipped})
}

// autoImportClusters runs the importer best-effort after a successful vault unlock. Any error
// is logged and swallowed so a kubeconfig problem never blocks unlocking the daemon.
func (d *Daemon) autoImportClusters() {
	added, skipped, err := d.importer.Import(k8s.DiscoverKubeconfigPaths())
	if err != nil {
		slog.Warn("kubeconfig auto-import failed (continuing — unlock unaffected)", "err", err)
		return
	}
	if len(added) > 0 {
		slog.Info("kubeconfig auto-import", "added", len(added), "skipped", len(skipped))
		d.publish("upstream.created", map[string]any{"count": len(added)})
	}
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
	out := make([]map[string]any, 0, len(ups))
	for _, u := range ups {
		m := map[string]any{
			"id": u.ID, "name": u.Name, "base_url": u.BaseURL, "auth_type": u.AuthType, "kind": u.Kind,
		} // secrets intentionally omitted
		if u.Kind == upstream.KindK8s {
			// Non-secret cluster metadata the Clusters UI needs: the auth method and whether TLS
			// verification is disabled (drives the red "insecure" badge). No credentials leak.
			m["k8s_auth"] = u.Auth.K8sAuth
			m["k8s_insecure"] = u.Auth.K8sInsecureSkipVerify
		}
		out = append(out, m)
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
		Outcome         string `json:"outcome"`
		RateLimitPerMin int    `json:"rate_limit_per_min"`
		// HTTP operation rule fields:
		OpMethod        string                        `json:"op_method"`
		OpPathTemplate  string                        `json:"op_path_template"`
		OpQueryTemplate map[string]string             `json:"op_query_template"`
		OpValuePolicies map[string]policy.ValuePolicy `json:"op_value_policies"`
		// k8s rule fields:
		Namespace string `json:"namespace"`
		Resource  string `json:"resource"`
		Verb      string `json:"verb"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	rule, err := d.policy.Create(policy.Rule{
		SubjectAgentID: body.SubjectAgentID, UpstreamID: body.UpstreamID,
		Outcome: body.Outcome, RateLimitPerMin: body.RateLimitPerMin,
		OpMethod: body.OpMethod, OpPathTemplate: body.OpPathTemplate,
		OpQueryTemplate: body.OpQueryTemplate, OpValuePolicies: body.OpValuePolicies,
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
			"op_method": rule.OpMethod, "op_path_template": rule.OpPathTemplate,
			"op_query_template": rule.OpQueryTemplate, "op_value_policies": rule.OpValuePolicies,
			"outcome": rule.Outcome, "rate_limit_per_min": rule.RateLimitPerMin,
			"namespace": rule.Namespace, "resource": rule.Resource, "verb": rule.Verb,
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

// hRuleSetVariablePolicy replaces a single variable's value policy on an operation rule — the
// Operations screen's add/remove-value and trust-any toggle. The UI computes the whole policy
// (set + values, or mode "any") and posts it; the registry keeps the variable's declared Type and
// rejects an unknown variable so the operator can never widen a rule by typo.
func (d *Daemon) hRuleSetVariablePolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Var    string             `json:"var"`
		Policy policy.ValuePolicy `json:"policy"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := d.policy.SetVariablePolicy(r.PathValue("id"), body.Var, body.Policy); err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d.publish("rule.updated", map[string]any{"id": r.PathValue("id")})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (d *Daemon) hApprovalList(w http.ResponseWriter, _ *http.Request) {
	pending := d.approvals.List()
	out := make([]map[string]any, 0, len(pending))
	for _, p := range pending {
		m := map[string]any{
			"id": p.ID, "agent_id": p.AgentID, "upstream_id": p.UpstreamID,
			"method": p.Method, "path": p.Path, "purpose": p.Purpose,
			"created_at": p.CreatedAt.Format(time.RFC3339Nano),
			// k8s tuple (empty for http approvals).
			"namespace": p.Namespace, "resource": p.Resource, "verb": p.Verb,
		}
		// MCP control-plane approval context (empty for data-plane / k8s approvals).
		if p.Kind != "" {
			m["kind"] = p.Kind
			m["host"] = p.Host
		}
		if p.Kind == approval.KindOperation {
			m["op_method"] = p.OpMethod
			m["op_path_template"] = p.OpPathTemplate
			m["op_query_template"] = p.OpQueryTemplate
			m["op_variables"] = p.OpVariables
			m["op_values"] = p.OpValues
		}
		// http new-value approval context (empty for k8s approvals).
		if len(p.NewValues) > 0 {
			m["new_values"] = p.NewValues
			m["template"] = p.Template
			m["rule_id"] = p.RuleID
		}
		// Surface the agent-sent patch/apply body with credentials masked. Never the injected
		// cluster credential — RequestBody is the agent's payload, captured before injection.
		if len(p.RequestBody) > 0 {
			m["request_body"] = audit.MaskBody(p.RequestBody)
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *Daemon) hApprovalResolve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Approve bool `json:"approve"`
		// Auth is the host credential the operator attaches when approving a KindHostAccess
		// request (optional — the operator may attach it later via the upstreams API).
		Auth *upstream.AuthConfig `json:"auth"`
		// TrustAny lists the operation variables the operator chose to trust for ANY value
		// (per-variable "approve + trust any value"); those flip to mode "any" instead of a set.
		TrustAny []string `json:"trust_any"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	id := r.PathValue("id")

	// Inspect the pending so MCP control-plane approvals (host / operation) run their side
	// effects before we unpark the waiter. Data-plane new-value / k8s approvals have empty Kind
	// and are resolved by the queue alone (unchanged).
	if p, ok := d.approvals.Get(id); ok && body.Approve {
		if err := d.applyApprovalSideEffects(p, body.Auth, body.TrustAny); err != nil {
			adminErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	switch err := d.approvals.Resolve(id, body.Approve); {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, approval.ErrNotFound):
		adminErr(w, http.StatusNotFound, "approval not found")
	default:
		adminErr(w, http.StatusInternalServerError, err.Error())
	}
}

// applyApprovalSideEffects runs the create/extend/attach work for an approved MCP control-plane
// approval (host or operation). It is a no-op for empty-Kind approvals (data-plane / k8s), whose
// side effects already live on the proxy path. Errors are reported before the queue is unparked,
// so a failed attach/rule-write does not silently approve.
func (d *Daemon) applyApprovalSideEffects(p approval.Pending, auth *upstream.AuthConfig, trustAny []string) error {
	switch p.Kind {
	case approval.KindHostAccess:
		// Attach the operator-entered credential to the lazily-created host upstream (optional).
		if auth != nil {
			if err := d.upstreams.SetAuth(p.UpstreamID, *auth); err != nil {
				return fmt.Errorf("attach host credential: %w", err)
			}
		}
		return nil
	case approval.KindOperation:
		return d.approveOperation(p, trustAny)
	default:
		return nil
	}
}

// approveOperation creates the H1 operation rule for the pending's template if no rule with the
// same template Key() exists on the upstream, otherwise extends the existing rule. For each text
// variable it adds the requested value to the allowed-set, OR flips the variable to mode "any"
// when it is listed in trustAny; date variables are mode "any". Reuses Registry.AddAllowedValue so
// approving a new value on an existing template grows that rule's set rather than spawning a new
// one.
func (d *Daemon) approveOperation(p approval.Pending, trustAny []string) error {
	tmpl, err := optemplate.Parse(p.OpMethod, p.OpPathTemplate, p.OpQueryTemplate)
	if err != nil {
		return fmt.Errorf("parse operation template: %w", err)
	}
	trust := map[string]bool{}
	for _, v := range trustAny {
		trust[v] = true
	}

	rule, err := d.findRuleByTemplateKey(p.UpstreamID, tmpl.Key())
	if err != nil {
		return err
	}
	if rule == nil {
		// Create the rule with a value policy per declared variable: date → any; text → any when
		// trusted, else a set seeded with the requested value (if any).
		policies := map[string]policy.ValuePolicy{}
		for _, v := range p.OpVariables {
			vp := policy.ValuePolicy{Type: v.Type}
			switch {
			case v.Type == string(optemplate.Date):
				vp.Mode = "any"
			case v.Type == string(optemplate.Number):
				// Default to any number; the operator tightens it to a [min,max] range in the UI.
				vp.Mode = "any"
			case trust[v.Name]:
				vp.Mode = "any"
			default:
				// text / enum: a set seeded with the requested value. For enum this set is CLOSED
				// (an out-of-set value is denied); for text it grows on later approvals.
				vp.Mode = "set"
				if val, ok := p.OpValues[v.Name]; ok && val != "" {
					vp.Values = []string{val}
				}
			}
			policies[v.Name] = vp
		}
		if _, err := d.policy.Create(policy.Rule{
			UpstreamID: p.UpstreamID, Outcome: policy.Allow,
			OpMethod: p.OpMethod, OpPathTemplate: p.OpPathTemplate, OpQueryTemplate: p.OpQueryTemplate,
			OpValuePolicies: policies,
		}); err != nil {
			return fmt.Errorf("create operation rule: %w", err)
		}
		return nil
	}

	// Extend the existing rule: flip trusted text vars to any, else add the requested value.
	for _, v := range p.OpVariables {
		if v.Type == string(optemplate.Date) || v.Type == string(optemplate.Number) {
			continue // date stays any; number range is operator-managed (values don't gate it)
		}
		if trust[v.Name] {
			if err := d.policy.SetVariableAny(rule.ID, v.Name); err != nil {
				return fmt.Errorf("trust-any variable %q: %w", v.Name, err)
			}
			continue
		}
		if val, ok := p.OpValues[v.Name]; ok && val != "" {
			if err := d.policy.AddAllowedValue(rule.ID, v.Name, val); err != nil {
				return fmt.Errorf("extend variable %q: %w", v.Name, err)
			}
		}
	}
	return nil
}

// findRuleByTemplateKey returns the upstream's http operation rule whose template Key() equals
// key, or nil if none exists. Two requests with the same (method, path-template, query-template)
// share one rule (the H1 identity), so this is how a re-approval finds the rule to extend.
func (d *Daemon) findRuleByTemplateKey(upstreamID, key string) (*policy.Rule, error) {
	rules, err := d.policy.ForUpstream(upstreamID)
	if err != nil {
		return nil, fmt.Errorf("load rules: %w", err)
	}
	for _, r := range rules {
		if r.OpPathTemplate == "" {
			continue // skip k8s rules
		}
		t, err := optemplate.Parse(r.OpMethod, r.OpPathTemplate, r.OpQueryTemplate)
		if err != nil {
			continue // a malformed stored template never matches
		}
		if t.Key() == key {
			return r, nil
		}
	}
	return nil, nil
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
		"operation": e.Operation, "vars": e.Vars,
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

func (d *Daemon) hAuditRetentionGet(w http.ResponseWriter, _ *http.Request) {
	days, err := d.audit.RetentionDays()
	if err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"days": days})
}

func (d *Daemon) hAuditRetentionSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Days int `json:"days"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if body.Days < 0 {
		adminErr(w, http.StatusBadRequest, "days must be >= 0")
		return
	}
	if err := d.audit.SetRetentionDays(body.Days); err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"days": body.Days})
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
