package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/audit"
	"github.com/Sipaha/outwall/internal/k8s"
	"github.com/Sipaha/outwall/internal/oidcdisc"
	"github.com/Sipaha/outwall/internal/optemplate"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/serverprofile"
	"github.com/Sipaha/outwall/internal/upstream"
)

// apiMux registers the admin API routes onto a fresh mux, split into an UNGATED group (read-only
// GETs, the SSE stream, the dry-run preset preview, the launcher focus hand-off, and the
// /operator/session/* control routes — the master-password entry point) and an OPERATOR-GATED group
// (every privileged mutation). Gated routes are wrapped per-route by operatorGate. Both transports —
// the unix socket (AdminHandler) and the TCP UI bind (UIHandler under /api) — build their handler
// from this same table, so the gate applies UNIFORMLY on both: a same-user process can no longer
// self-approve or self-grant over either. (ADR-0041; replaces the ADR-0005 X-Outwall-CSRF model.)
func (d *Daemon) apiMux() *http.ServeMux {
	mux := http.NewServeMux()

	// --- UNGATED: read-only + the operator-session control routes (they grant no power). ---
	mux.HandleFunc("GET /vault/status", d.hVaultStatus)
	// POST /vault/init is the bootstrap that ESTABLISHES the master password — it cannot sit behind
	// the gate (no password exists yet) and opens the session on success (see hVaultInit).
	mux.HandleFunc("POST /vault/init", d.hVaultInit)
	// POST /vault/unlock is UNGATED for the same reason as init: it already requires the master
	// password (the vault verifies it), so an operator-session gate on top is redundant and would
	// double-prompt the operator (unlock 403 -> session modal -> re-enter the same password). It
	// opens the session on success (see hVaultUnlock) — the operator just proved the master password.
	mux.HandleFunc("POST /vault/unlock", d.hVaultUnlock)
	mux.HandleFunc("GET /upstreams", d.hUpstreamList)
	mux.HandleFunc("GET /oidc/redirect-uri", d.hOIDCRedirectURI)
	mux.HandleFunc("GET /agents", d.hAgentList)
	mux.HandleFunc("GET /rules", d.hRuleList)
	mux.HandleFunc("GET /profiles", d.hProfileList)
	mux.HandleFunc("POST /presets/preview", d.hPresetPreview) // dry-run, no state change
	mux.HandleFunc("GET /approvals", d.hApprovalList)
	mux.HandleFunc("GET /access-requests", d.hAccessRequestList)
	mux.HandleFunc("GET /audit", d.hAuditList)
	mux.HandleFunc("GET /audit/{id}", d.hAuditGet)
	mux.HandleFunc("GET /settings/audit-retention", d.hAuditRetentionGet)
	mux.HandleFunc("GET /events", sseHandler(d.bus))
	mux.HandleFunc("POST /desktop/focus", d.hDesktopFocus) // single-instance launcher hand-off
	mux.HandleFunc("POST /operator/session/open", d.hOperatorSessionOpen)
	mux.HandleFunc("POST /operator/session/lock", d.hOperatorSessionLock)
	mux.HandleFunc("GET /operator/session/status", d.hOperatorSessionStatus)

	// --- OPERATOR-GATED: privileged mutations. Each requires an open operator session, on BOTH
	//     transports (per-route wrapping is inherited by AdminHandler AND UIHandler). ---
	gate := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, d.operatorGate(h))
	}
	gate("POST /vault/lock", d.hVaultLock)
	gate("POST /upstreams", d.hUpstreamCreate)
	gate("DELETE /upstreams/{name}", d.hUpstreamDelete)
	gate("POST /upstreams/{name}/auth", d.hUpstreamSetAuth)
	gate("POST /upstreams/{name}/oauth/login", d.hOAuthLogin)
	gate("POST /oidc/discover", d.hOIDCDiscover)
	gate("POST /agents/register", d.hAgentRegister)
	gate("DELETE /agents/{id}", d.hAgentDelete)
	gate("POST /clusters/import", d.hClustersImport)
	gate("POST /kubeconfig", d.hKubeconfig)
	gate("POST /rules", d.hRuleCreate)
	gate("DELETE /rules/{id}", d.hRuleDelete)
	gate("POST /rules/{id}/value-policy", d.hRuleSetVariablePolicy)
	gate("POST /approvals/{id}/resolve", d.hApprovalResolve)
	gate("POST /access-requests/{id}/resolve", d.hAccessRequestResolve)
	gate("POST /audit/prune", d.hAuditPrune)
	gate("PUT /settings/audit-retention", d.hAuditRetentionSet)
	return mux
}

// operatorGate wraps a privileged route so it is served only while the operator session is open
// (unlocked by the master password — a secret the same-user agent does not hold). Otherwise it
// returns 403 with {"error":"operator session required"}, which the CLI (sudo-style prompt) and the
// web UI (master-password modal) recognize to trigger a re-open. A successful Authorized() call
// slides the idle window (see internal/opsession).
func (d *Daemon) operatorGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.opsession.Authorized() {
			adminErr(w, http.StatusForbidden, "operator session required")
			return
		}
		next.ServeHTTP(w, r)
	})
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

// AdminHandler builds the admin API mux served over the unix socket for the local operator CLI.
// Privileged routes are gated by the operator session exactly as on the UI transport (apiMux).
func (d *Daemon) AdminHandler() http.Handler { return d.apiMux() }

// UIHandler builds the desktop-UI handler served over the UIListen TCP bind: the embedded SPA at
// "/", the shared admin/SSE mux under "/api" (privileged routes gated by the operator session — see
// apiMux/operatorGate), and the OIDC browser-login redirect target. There is no CSRF wrapper: the
// operator-session gate (master password) replaced the X-Outwall-CSRF model (ADR-0041 amends
// ADR-0005). GET /api/events (SSE) stays reachable — it is ungated and read-only.
func (d *Daemon) UIHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", d.apiMux()))
	// The OIDC browser-login redirect target is served top-level (not under /api): a redirect from
	// the IdP cannot carry app headers, and the random state ties it to a login this daemon started
	// (see oauth.go). The POST that STARTS the login (/api/upstreams/{name}/oauth/login) is gated.
	mux.HandleFunc("/oauth/callback", d.hOAuthCallback)
	mux.Handle("/", staticUI())
	return mux
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
	// Init is the bootstrap that ESTABLISHES the master password, so it cannot itself sit behind
	// the operator-session gate (there is no password yet). The operator just set the password and
	// is present, so open their session here — subsequent gated calls need no immediate re-prompt.
	d.opsession.Open()
	// Init leaves the vault unlocked. First run is exactly when there is nothing yet, so this is
	// the ONE place we auto-import the host's kubeconfig clusters (now that the vault can encrypt
	// their auth) — seeding an empty vault. A failure must NEVER fail the init (logged only).
	// Later clusters are added/refreshed via the explicit Import button, not on unlock (ADR-0026).
	d.publish("vault.unlocked", map[string]any{})
	d.autoImportClusters()
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
		// Unlocking proves the operator holds the master password, so open their operator session
		// here (mirroring hVaultInit). This is why /vault/unlock is ungated: it is self-authenticating
		// and would otherwise double-prompt (unlock gate 403 -> session modal for the same password).
		d.opsession.Open()
		d.publish("vault.unlocked", map[string]any{})
		// NOTE: no kubeconfig auto-import here. The scan runs ONLY at init (first run) — re-running
		// it on every unlock would risk clobbering operator state and is unnecessary. To pull in
		// new/changed clusters later the operator uses the explicit Import button (ADR-0026).
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

// hOperatorSessionOpen verifies the master password (WITHOUT unlocking the vault — the data plane is
// unaffected) and opens the operator session. This route is UNGATED: it IS the master-password entry
// point that authorizes the gated routes. Wrong password → 401; no vault yet → 400.
func (d *Daemon) hOperatorSessionOpen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	switch err := d.vault.Verify(body.Password); {
	case err == nil:
		d.opsession.Open()
		open, idle := d.opsession.Status()
		writeJSON(w, http.StatusOK, map[string]any{"open": open, "idle_remaining_seconds": int(idle.Seconds())})
	case errors.Is(err, secret.ErrBadPassword):
		adminErr(w, http.StatusUnauthorized, "incorrect master password")
	case errors.Is(err, secret.ErrNotInitialized):
		adminErr(w, http.StatusBadRequest, "vault not initialized")
	default:
		adminErr(w, http.StatusInternalServerError, err.Error())
	}
}

// hOperatorSessionLock closes the operator session ("Lock now"). It does NOT lock the vault — the
// data plane keeps serving; only privileged operator mutations become unavailable until the next open.
func (d *Daemon) hOperatorSessionLock(w http.ResponseWriter, _ *http.Request) {
	d.opsession.Lock()
	writeJSON(w, http.StatusOK, map[string]bool{"open": false})
}

// hOperatorSessionStatus reports whether the operator session is open and the idle time remaining.
// Read-only peek — it does not slide the idle window.
func (d *Daemon) hOperatorSessionStatus(w http.ResponseWriter, _ *http.Request) {
	open, idle := d.opsession.Status()
	writeJSON(w, http.StatusOK, map[string]any{"open": open, "idle_remaining_seconds": int(idle.Seconds())})
}

func (d *Daemon) hUpstreamCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string              `json:"name"`
		BaseURL string              `json:"base_url"`
		Kind    string              `json:"kind"`
		Profile string              `json:"profile"`
		Auth    upstream.AuthConfig `json:"auth"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	up, err := d.upstreams.CreateProfiled(body.Name, body.BaseURL, body.Kind, body.Profile, body.Auth)
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
	// Replacing a credential of the SAME type keeps the existing secrets the operator left blank
	// (the UI never receives them) and the server-managed OIDC tokens — so editing one field (e.g.
	// the client id) does not wipe the secret or force a re-login. A different type = a full
	// reconfigure, taken as sent.
	merged := mergeKeepSecrets(up.Auth, body.Auth)
	if err := d.upstreams.SetAuth(up.ID, merged); err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d.publish("upstream.updated", map[string]any{"id": up.ID, "name": up.Name})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// maxImportBody caps an uploaded kubeconfig at 1 MiB — far above any real kubeconfig, small
// enough to reject a runaway upload.
const maxImportBody = 1 << 20

// hClustersImport imports clusters at the operator's explicit request. When the request carries a
// body it is treated as an uploaded kubeconfig (the file-picker path) and imported via
// ImportContent; otherwise it re-scans the host's kubeconfig files. Either way it is an explicit
// operator action, so update=true: an existing cluster is refreshed in place (server + auth on the
// same upstream ID — its rules survive), which is how the operator repairs or rotates credentials
// (see ADR-0026). Returns the (non-nil) names added/updated/skipped. Requires the vault unlocked.
func (d *Daemon) hClustersImport(w http.ResponseWriter, r *http.Request) {
	body, readErr := io.ReadAll(io.LimitReader(r.Body, maxImportBody))
	if readErr != nil {
		adminErr(w, http.StatusBadRequest, "read request body")
		return
	}

	var added, updated, skipped []string
	var err error
	if len(body) > 0 {
		// Operator-uploaded kubeconfig: no baseDir (managed configs use inline *-data).
		added, updated, skipped, err = d.importer.ImportContent(body, "", true)
	} else {
		added, updated, skipped, err = d.importer.Import(k8s.DiscoverKubeconfigPaths(), true)
	}
	if err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(added) > 0 || len(updated) > 0 {
		d.publish("upstream.created", map[string]any{"count": len(added) + len(updated)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "updated": updated, "skipped": skipped})
}

// autoImportClusters seeds clusters from the host's kubeconfig on first run (vault init), best
// effort. It runs ONLY at init — not on unlock — because the scan is meant to seed an empty vault,
// not to re-run on every unlock and risk clobbering operator state (see ADR-0026). update=false:
// any name already present is skipped. Any error is logged and swallowed so a kubeconfig problem
// never blocks init.
func (d *Daemon) autoImportClusters() {
	added, _, skipped, err := d.importer.Import(k8s.DiscoverKubeconfigPaths(), false)
	if err != nil {
		slog.Warn("kubeconfig auto-import failed (continuing — init unaffected)", "err", err)
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
			"id": u.ID, "name": u.Name, "base_url": u.BaseURL, "auth_type": u.AuthType,
			"kind": u.Kind, "profile": u.Profile,
		} // secrets intentionally omitted
		if u.Kind == upstream.KindK8s {
			// Non-secret cluster metadata the Clusters UI needs: the auth method and whether TLS
			// verification is disabled (drives the red "insecure" badge). No credentials leak.
			m["k8s_auth"] = u.Auth.K8sAuth
			m["k8s_insecure"] = u.Auth.K8sInsecureSkipVerify
		} else {
			// Non-secret auth settings so the "replace credential" form can pre-fill (secrets cleared).
			m["auth"] = u.Auth.Public()
			// For an OIDC browser-login host, whether a login has actually completed (tokens held) —
			// distinct from merely being configured. Drives the "logged in" vs "needs login" badge.
			if u.Auth.Type == "oidc-authorization-code" {
				m["logged_in"] = u.Auth.AccessToken != "" || u.Auth.RefreshToken != ""
			}
			if bu := d.browseURLFor(u); bu != "" {
				m["browse_url"] = bu
			}
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
		lastSeen := ""
		if !a.LastSeenAt.IsZero() {
			lastSeen = a.LastSeenAt.Format(time.RFC3339Nano)
		}
		out = append(out, map[string]string{
			"id": a.ID, "name": a.Name, "status": a.Status,
			"created_at": a.CreatedAt.Format(time.RFC3339Nano), "last_seen_at": lastSeen,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// hAgentDelete removes an agent and cascades: every policy rule granted to it is deleted first, so
// no orphaned grant survives (a stale rule referencing a gone agent id would be inert but
// confusing in the Rules list). Gated (operator session required) — see apiMux.
func (d *Daemon) hAgentDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := d.policy.DeleteBySubject(id); err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := d.agents.Delete(id); err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.publish("agent.deleted", map[string]any{"id": id})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
		OpBodyTemplate  map[string]string             `json:"op_body_template"`
		OpValuePolicies map[string]policy.ValuePolicy `json:"op_value_policies"`
		// k8s rule fields:
		Namespace string `json:"namespace"`
		Resource  string `json:"resource"`
		Verb      string `json:"verb"`
		// server-profile rule fields:
		Profile       string          `json:"profile"`
		ProfileParams json.RawMessage `json:"profile_params"`
		// browse policy fields:
		BrowseMethods string `json:"browse_methods"`
		BrowsePath    string `json:"browse_path"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	rule, err := d.policy.Create(policy.Rule{
		SubjectAgentID: body.SubjectAgentID, UpstreamID: body.UpstreamID,
		Outcome: body.Outcome, RateLimitPerMin: body.RateLimitPerMin,
		OpMethod: body.OpMethod, OpPathTemplate: body.OpPathTemplate,
		OpQueryTemplate: body.OpQueryTemplate, OpBodyTemplate: body.OpBodyTemplate,
		OpValuePolicies: body.OpValuePolicies,
		Namespace:       body.Namespace, Resource: body.Resource, Verb: body.Verb,
		Profile: body.Profile, ProfileParams: body.ProfileParams,
		BrowseMethods: body.BrowseMethods, BrowsePath: body.BrowsePath,
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
			"op_query_template": rule.OpQueryTemplate, "op_body_template": rule.OpBodyTemplate,
			"op_value_policies": rule.OpValuePolicies,
			"outcome":           rule.Outcome, "rate_limit_per_min": rule.RateLimitPerMin,
			"namespace": rule.Namespace, "resource": rule.Resource, "verb": rule.Verb,
			"profile": rule.Profile, "profile_params": rule.ProfileParams,
			"browse_methods": rule.BrowseMethods, "browse_path": rule.BrowsePath,
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

// hProfileList returns the registered server profiles and their rule schemas, so the UI can render
// the right rule editor per profile. "raw-http" is implicit and not listed here.
func (d *Daemon) hProfileList(w http.ResponseWriter, _ *http.Request) {
	out := make([]serverprofile.RuleSchema, 0)
	for _, name := range serverprofile.Names() {
		if p, ok := serverprofile.Get(name); ok {
			out = append(out, p.RuleSchema())
		}
	}
	writeJSON(w, http.StatusOK, out)
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
		if p.Kind == approval.KindK8sAccess && len(p.K8sGrants) > 0 {
			m["k8s_grants"] = p.K8sGrants
		}
		if p.Kind == approval.KindOperation {
			m["op_method"] = p.OpMethod
			m["op_path_template"] = p.OpPathTemplate
			m["op_query_template"] = p.OpQueryTemplate
			m["op_body_template"] = p.OpBodyTemplate
			m["op_variables"] = p.OpVariables
			m["op_values"] = p.OpValues
		}
		if p.Kind == approval.KindPreset {
			m["preset_id"] = p.PresetID
			m["bindings"] = p.Bindings
			if preset, ok, perr := d.presetForUpstream(p.UpstreamID, p.PresetID); perr == nil && ok {
				m["preset"] = preset // serializes {id,label,slots} (Build is json:"-")
			}
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
		// Reason is the operator's optional explanation when denying — surfaced to the agent
		// (the blocked data-plane response, or get_access for an MCP request). Ignored on approve.
		Reason string `json:"reason"`
		// Auth is the host credential the operator attaches when approving a KindHostAccess
		// request (optional — the operator may attach it later via the upstreams API).
		Auth *upstream.AuthConfig `json:"auth"`
		// TrustAny lists the operation variables the operator chose to trust for ANY value
		// (per-variable "approve + trust any value"); those flip to mode "any" instead of a set.
		TrustAny []string `json:"trust_any"`
		// Bindings are the operator's final preset slot values for a KindPreset approval (may narrow
		// the agent's requested values). Ignored for other kinds.
		Bindings map[string]string `json:"bindings"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	id := r.PathValue("id")

	// Inspect the pending so MCP control-plane approvals (host / operation) run their side
	// effects before we unpark the waiter. Data-plane new-value / k8s approvals have empty Kind
	// and are resolved by the queue alone (unchanged).
	if p, ok := d.approvals.Get(id); ok {
		if body.Approve {
			if err := d.applyApprovalSideEffects(p, body.Auth, body.TrustAny, body.Bindings); err != nil {
				adminErr(w, http.StatusBadRequest, err.Error())
				return
			}
			// Keep the access-request history in sync with the card decision so the table is a
			// faithful read-only log (ADR-0025). MCP kinds (host/operation/k8s) log an intent row.
			if p.Kind != "" {
				if _, gerr := d.access.GrantLatest(p.AgentID, p.UpstreamID); gerr != nil {
					slog.Warn("record grant", "err", gerr)
				}
			}
		} else if p.Kind != "" {
			// MCP host/operation/k8s deny: mark the agent's latest access-request denied (with the
			// reason, if any) so the agent learns the outcome when it polls get_access and the table
			// history matches the decision (the data-plane path gets the reason via the queue).
			if _, derr := d.access.DenyLatest(p.AgentID, p.UpstreamID, body.Reason); derr != nil {
				slog.Warn("record deny", "err", derr)
			}
		}
	}

	switch err := d.approvals.Resolve(id, body.Approve, body.Reason); {
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
func (d *Daemon) applyApprovalSideEffects(p approval.Pending, auth *upstream.AuthConfig, trustAny []string, bindings map[string]string) error {
	switch p.Kind {
	case approval.KindHostAccess:
		// Attach the operator-entered credential to the lazily-created host upstream (optional).
		if auth != nil {
			// Defense-in-depth: never let a host credential overwrite a k8s cluster's auth. A k8s
			// cluster is pre-credentialed and SetAuth replaces the WHOLE auth config — a static
			// header would wipe its k8s_auth and break the data plane. request_host_access no longer
			// raises a host card for k8s (ADR-0025), so this guards a card that should never exist.
			up, err := d.upstreams.GetByID(p.UpstreamID)
			if err != nil {
				return fmt.Errorf("load upstream: %w", err)
			}
			if up.Kind == upstream.KindK8s {
				return fmt.Errorf("refusing to attach an HTTP credential to k8s cluster %q — re-import its kubeconfig instead", up.Name)
			}
			if err := d.upstreams.SetAuth(p.UpstreamID, *auth); err != nil {
				return fmt.Errorf("attach host credential: %w", err)
			}
		}
		return nil
	case approval.KindOperation:
		return d.approveOperation(p, trustAny)
	case approval.KindK8sAccess:
		return d.approveK8sAccess(p)
	case approval.KindPreset:
		return d.approvePreset(p, bindings)
	default:
		return nil
	}
}

// approveK8sAccess creates an agent-scoped allow k8s rule for each (namespace, resource, verb) tuple
// on the pending. Grants are scoped to the requesting agent (not the whole cluster) — see ADR-0025.
// Idempotent: a tuple whose identical rule already exists (same agent) is skipped (ADR-0029).
func (d *Daemon) approveK8sAccess(p approval.Pending) error {
	rules, err := d.policy.ForUpstream(p.UpstreamID)
	if err != nil {
		return fmt.Errorf("load rules: %w", err)
	}
	exists := func(g approval.K8sGrant) bool {
		for _, r := range rules {
			if r.SubjectAgentID == p.AgentID && r.OpPathTemplate == "" &&
				r.Namespace == g.Namespace && r.Resource == g.Resource && r.Verb == g.Verb {
				return true
			}
		}
		return false
	}
	grants := p.K8sGrants
	if len(grants) == 0 { // legacy single-tuple pending
		grants = []approval.K8sGrant{{Namespace: p.Namespace, Resource: p.Resource, Verb: p.Verb}}
	}
	missing := make([]policy.Rule, 0, len(grants))
	for _, g := range grants {
		if exists(g) {
			continue
		}
		missing = append(missing, policy.Rule{
			SubjectAgentID: p.AgentID, UpstreamID: p.UpstreamID, Outcome: policy.Allow,
			Namespace: g.Namespace, Resource: g.Resource, Verb: g.Verb,
		})
	}
	if len(missing) == 0 {
		return nil
	}
	if _, err := d.policy.CreateMany(missing); err != nil {
		return fmt.Errorf("create k8s rules: %w", err)
	}
	return nil
}

// approveOperation creates the H1 operation rule for the pending's template if no rule with the
// same template Key() exists on the upstream, otherwise extends the existing rule. For each text
// variable it adds the requested value to the allowed-set, OR flips the variable to mode "any"
// when it is listed in trustAny; date variables are mode "any". Reuses Registry.AddAllowedValue so
// approving a new value on an existing template grows that rule's set rather than spawning a new
// one.
func (d *Daemon) approveOperation(p approval.Pending, trustAny []string) error {
	tmpl, err := optemplate.ParseWithBody(p.OpMethod, p.OpPathTemplate, p.OpQueryTemplate, p.OpBodyTemplate)
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
			OpBodyTemplate: p.OpBodyTemplate, OpValuePolicies: policies,
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
			"purpose": req.Purpose, "status": req.Status, "reason": req.Reason,
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

// hOIDCDiscover fetches an OpenID Connect provider's discovery document for an operator-entered
// issuer (or full discovery) URL and returns the endpoints, so the Add-host form can auto-fill the
// OIDC fields. Generic OIDC — no provider-specific handling.
func (d *Daemon) hOIDCDiscover(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	// Operator-only fetch; bound it with a timeout and a small redirect cap (an OIDC well-known
	// document needs at most an issuer-normalization hop — don't follow a long redirect chain).
	hc := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	cfg, err := oidcdisc.Discover(r.Context(), hc, body.URL)
	if err != nil {
		adminErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                 cfg.Issuer,
		"authorization_endpoint": cfg.AuthorizationEndpoint,
		"token_endpoint":         cfg.TokenEndpoint,
		"end_session_endpoint":   cfg.EndSessionEndpoint,
		"scopes_supported":       cfg.ScopesSupported,
	})
}

// mergeKeepSecrets fills the incoming auth's blank secret fields (and the server-managed OIDC
// tokens) from the existing config when the auth type is unchanged — so a "replace credential" edit
// that leaves a secret blank keeps the stored one rather than wiping it. A changed type is a full
// reconfigure: take the incoming config verbatim.
func mergeKeepSecrets(old, in upstream.AuthConfig) upstream.AuthConfig {
	if old.Type != in.Type {
		return in
	}
	keep := func(dst *string, prev string) {
		if *dst == "" {
			*dst = prev
		}
	}
	keep(&in.Token, old.Token)
	keep(&in.Password, old.Password)
	keep(&in.ClientSecret, old.ClientSecret)
	keep(&in.AWSSecretAccessKey, old.AWSSecretAccessKey)
	keep(&in.HMACSecret, old.HMACSecret)
	keep(&in.ClientKey, old.ClientKey)
	// OIDC tokens are server-managed and never sent from the form — always carry them over.
	keep(&in.AccessToken, old.AccessToken)
	keep(&in.RefreshToken, old.RefreshToken)
	keep(&in.TokenExpiry, old.TokenExpiry)
	return in
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

// dnsHostRe matches names safe for use as a DNS label prefix (no @, /, etc.).
var dnsHostRe = regexp.MustCompile(`^[A-Za-z0-9.\-]+$`)

// isDNSHost reports whether name is non-empty and contains only DNS-safe characters.
// k8s/exec upstream names like "kubernetes-admin@kubernetes" contain "@" and return false.
func isDNSHost(name string) bool {
	return name != "" && dnsHostRe.MatchString(name)
}

// browseURLFor returns the per-upstream browser origin for an http upstream, or "" when
// browsing does not apply (no browse domain configured, a k8s cluster, or a non-DNS name).
// The port is extracted from cfg.Listen (e.g. "127.0.0.1:8080" → "8080").
func (d *Daemon) browseURLFor(u *upstream.Upstream) string {
	if d.cfg.BrowseDomain == "" || u.Kind == upstream.KindK8s || !isDNSHost(u.Name) {
		return ""
	}
	_, port, err := net.SplitHostPort(d.cfg.Listen)
	if err != nil {
		port = ""
	}
	host := u.Name + "." + d.cfg.BrowseDomain
	if port == "" {
		return "https://" + host
	}
	return "https://" + host + ":" + port
}
