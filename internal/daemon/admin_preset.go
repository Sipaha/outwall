package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/serverprofile"
	"github.com/Sipaha/outwall/internal/upstream"
)

// approvePreset expands a KindPreset approval into agent-scoped allow rules. The operator may have
// narrowed the slot values (bindings); when nil the agent's requested bindings are used. The final
// bindings are re-validated against the preset's slot schema before any rule is created (ADR-0037).
func (d *Daemon) approvePreset(p approval.Pending, bindings map[string]string) error {
	final := bindings
	if final == nil {
		final = p.Bindings
	}
	preset, ok, err := d.presetForUpstream(p.UpstreamID, p.PresetID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown preset %q for upstream", p.PresetID)
	}
	if err := serverprofile.ValidateBindings(preset.Slots, serverprofile.Bindings(final)); err != nil {
		return fmt.Errorf("invalid preset bindings: %w", err)
	}
	tmpls, err := preset.Build(serverprofile.Bindings(final))
	if err != nil {
		return fmt.Errorf("expand preset: %w", err)
	}
	// Idempotent (ADR-0029, like approveK8sAccess/approveOperation): skip any preset rule identical
	// to one this agent already holds on the upstream. Otherwise re-approving a preset — or
	// approving two presets that share a rule (e.g. browse-get and citeck-readonly both grant
	// `allow browse GET,HEAD /**`) — spawns duplicate rules.
	existing, err := d.policy.ForUpstream(p.UpstreamID)
	if err != nil {
		return fmt.Errorf("load rules: %w", err)
	}
	// Empty profile params are persisted as "{}" (policy.Registry.Create), so normalise before
	// comparing — a freshly-built browse rule carries "" and must still match the stored "{}".
	normParams := func(pp json.RawMessage) string {
		if s := string(pp); s != "" && s != "{}" {
			return s
		}
		return "{}"
	}
	isDup := func(r policy.Rule) bool {
		for _, e := range existing {
			if e.SubjectAgentID == p.AgentID && e.Outcome == r.Outcome &&
				e.BrowseMethods == r.BrowseMethods && e.BrowsePath == r.BrowsePath &&
				e.Profile == r.Profile && normParams(e.ProfileParams) == normParams(r.ProfileParams) {
				return true
			}
		}
		return false
	}
	rules := make([]policy.Rule, 0, len(tmpls))
	for _, t := range tmpls {
		r := policy.Rule{
			SubjectAgentID: p.AgentID, UpstreamID: p.UpstreamID, Outcome: t.Outcome,
			BrowseMethods: t.BrowseMethods, BrowsePath: t.BrowsePath,
			Profile: t.Profile, ProfileParams: t.ProfileParams,
		}
		if isDup(r) {
			continue
		}
		rules = append(rules, r)
	}
	if len(rules) == 0 {
		return nil
	}
	if _, err := d.policy.CreateMany(rules); err != nil {
		return fmt.Errorf("create preset rules: %w", err)
	}
	return nil
}

// presetForUpstream resolves a preset id against an upstream's available catalog (core http presets
// for non-k8s + the upstream's profile presets).
func (d *Daemon) presetForUpstream(upstreamID, presetID string) (serverprofile.Preset, bool, error) {
	up, err := d.upstreams.GetByID(upstreamID)
	if err != nil {
		return serverprofile.Preset{}, false, fmt.Errorf("load upstream: %w", err)
	}
	preset, ok := serverprofile.FindPreset(up.Kind != upstream.KindK8s, up.Profile, presetID)
	return preset, ok, nil
}

// hPresetPreview dry-runs a preset's Build with the given bindings and returns a human-readable
// summary per rule it would create — so the approval card shows the concrete grant (reflecting any
// operator edits) before approving. It creates nothing.
func (d *Daemon) hPresetPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UpstreamID string            `json:"upstream_id"`
		PresetID   string            `json:"preset_id"`
		Bindings   map[string]string `json:"bindings"`
	}
	if err := decode(r, &body); err != nil {
		adminErr(w, http.StatusBadRequest, "bad json")
		return
	}
	preset, ok, err := d.presetForUpstream(body.UpstreamID, body.PresetID)
	if err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		adminErr(w, http.StatusBadRequest, "unknown preset")
		return
	}
	if err := serverprofile.ValidateBindings(preset.Slots, serverprofile.Bindings(body.Bindings)); err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	tmpls, err := preset.Build(serverprofile.Bindings(body.Bindings))
	if err != nil {
		adminErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rules := make([]string, 0, len(tmpls))
	for _, t := range tmpls {
		rules = append(rules, summarizeTemplate(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

// summarizeTemplate renders a one-line human summary of a preset rule template for the preview.
func summarizeTemplate(t serverprofile.RuleTemplate) string {
	switch {
	case t.BrowsePath != "":
		methods := t.BrowseMethods
		if methods == "" {
			methods = "*"
		}
		return fmt.Sprintf("%s browse %s %s", t.Outcome, methods, t.BrowsePath)
	case t.Profile != "":
		return fmt.Sprintf("%s %s %s", t.Outcome, t.Profile, string(t.ProfileParams))
	default:
		return t.Outcome
	}
}
