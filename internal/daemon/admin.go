package daemon

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/upstream"
)

// AdminHandler builds the admin API mux (served over the unix socket).
func (d *Daemon) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /vault/init", d.hVaultInit)
	mux.HandleFunc("POST /vault/unlock", d.hVaultUnlock)
	mux.HandleFunc("GET /vault/status", d.hVaultStatus)
	mux.HandleFunc("POST /upstreams", d.hUpstreamCreate)
	mux.HandleFunc("GET /upstreams", d.hUpstreamList)
	mux.HandleFunc("POST /agents/register", d.hAgentRegister)
	mux.HandleFunc("GET /agents", d.hAgentList)
	mux.HandleFunc("POST /grants", d.hGrantAdd)
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
		writeJSON(w, http.StatusOK, map[string]bool{"locked": false})
	case errors.Is(err, secret.ErrBadPassword):
		adminErr(w, http.StatusUnauthorized, "incorrect master password")
	default:
		adminErr(w, http.StatusBadRequest, err.Error())
	}
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
		Auth    upstream.AuthConfig `json:"auth"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	up, err := d.upstreams.Create(body.Name, body.BaseURL, body.Auth)
	if err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": up.ID})
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
			"id": u.ID, "name": u.Name, "base_url": u.BaseURL, "auth_type": u.AuthType,
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

func (d *Daemon) hGrantAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AgentID    string `json:"agent_id"`
		UpstreamID string `json:"upstream_id"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := d.grants.Add(body.AgentID, body.UpstreamID); err != nil {
		adminErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
