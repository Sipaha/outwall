// Package citeck is the server-profile plugin for Citeck ECOS upstreams. It classifies and gates
// Records API requests (POST /api/records/{query,mutate,delete}). It is the ONLY package in outwall
// permitted to name "citeck" (see ADR-0034); the core stays platform-agnostic. It also narrows a
// browser-originated read query to the agent's allowed workspaces (or returns an empty result)
// instead of denying the whole request — see ADR-0039.
package citeck

import (
	"encoding/json"
	"strings"

	"github.com/Sipaha/outwall/internal/serverprofile"
)

// Scope sentinels (profile-internal; opaque to the core). A concrete workspace id is any other
// string.
const (
	scopeAll     = "\x00all"     // request spans ALL workspaces (read with no workspaces filter)
	scopeUnknown = "\x00unknown" // workspace not derivable from the body (update / delete / get-atts)
)

// refSource splits an EntityRef "appName/sourceId@localId" into its source part (keeping the
// optional "appName/" prefix, which operator globs match against) and its localId. With no '@' the
// whole string is the localId and the source is empty (mirrors EntityRef.valueOf).
func refSource(ref string) (source, localID string) {
	at := strings.IndexByte(ref, '@')
	if at < 0 {
		return "", ref
	}
	return ref[:at], ref[at+1:]
}

// recordsOp returns the Records operation ("query"/"mutate"/"delete") for a path, matching the
// /records/{op} suffix to cover all gateway variants (/api/records/op, /gateway/records/op,
// /gateway/emodel/api/records/op, etc.).
//
// Safety note: the suffix match is intentionally loose (no anchored prefix). This is safe because
// classify is only called for POST requests (the Classify entry point rejects everything else
// immediately), and the Citeck gateway always routes Records calls through a /records/ path
// segment. A non-Records endpoint whose URL happens to end in "/records/query" is implausible in
// practice; if it ever arose it would simply be routed through the Records gating path, which is
// the more conservative outcome (deny unless an explicit read rule matches).
func recordsOp(path string) (string, bool) {
	for _, op := range []string{"query", "mutate", "delete"} {
		if strings.HasSuffix(path, "/records/"+op) {
			return op, true
		}
	}
	return "", false
}

func classify(r serverprofile.Request) (serverprofile.Operation, bool, error) {
	if r.Method != "POST" {
		return serverprofile.Operation{}, false, nil
	}
	op, ok := recordsOp(r.Path)
	if !ok {
		return serverprofile.Operation{}, false, nil
	}
	out := serverprofile.Operation{Method: r.Method, Path: r.Path}
	switch op {
	case "query":
		out.Kind = "read"
		var body struct {
			Query struct {
				SourceID   string   `json:"sourceId"`
				EcosType   string   `json:"ecosType"`
				Workspaces []string `json:"workspaces"`
			} `json:"query"`
			Records []string `json:"records"` // get-atts mode (multiple records)
			Record  string   `json:"record"`  // get-atts mode (single record; attributes is an object)
		}
		if err := json.Unmarshal(nonNil(r.Body), &body); err != nil {
			return serverprofile.Operation{}, false, nil // malformed → not handled (never grants)
		}
		if body.Query.SourceID != "" || body.Query.EcosType != "" {
			scopes := workspaceScopes(body.Query.Workspaces)
			// sourceId is already a bare source; ecosType is a ref ("src@localId") — normalize to
			// source-only so an operator rule "source_id: emodel/type" gates both identically.
			var sources []string
			if body.Query.SourceID != "" {
				sources = append(sources, body.Query.SourceID)
			}
			if body.Query.EcosType != "" {
				sources = append(sources, normalizeSource(body.Query.EcosType))
			}
			for _, src := range sources {
				for _, ws := range scopes {
					out.Resources = append(out.Resources, serverprofile.ResourceScope{Resource: src, Scope: ws})
				}
			}
		} else {
			// get-atts: read specific records by ref; workspace not derivable. The API accepts either
			// "records": [ref, ...] (attributes is a list) or the single-record "record": ref form
			// (attributes is an object) — both are reads and must be gated identically.
			refs := body.Records
			if body.Record != "" {
				refs = append(refs, body.Record)
			}
			for _, ref := range refs {
				src, _ := refSource(ref)
				out.Resources = append(out.Resources, serverprofile.ResourceScope{Resource: src, Scope: scopeUnknown})
			}
		}
	case "mutate":
		out.Kind = "write"
		var body struct {
			Records []struct {
				ID         string         `json:"id"`
				Attributes map[string]any `json:"attributes"`
			} `json:"records"`
		}
		if err := json.Unmarshal(nonNil(r.Body), &body); err != nil {
			return serverprofile.Operation{}, false, nil
		}
		for _, rec := range body.Records {
			src, localID := refSource(rec.ID)
			scope := scopeUnknown
			if localID == "" { // create: workspace may be set explicitly
				if ws, _ := rec.Attributes["_workspace"].(string); ws != "" {
					_, wsLocal := refSource(ws) // _workspace may be a ref; take its localId
					if wsLocal != "" {
						scope = wsLocal
					} else {
						scope = ws
					}
				}
			}
			out.Resources = append(out.Resources, serverprofile.ResourceScope{Resource: src, Scope: scope})
		}
	case "delete":
		out.Kind = "write"
		var body struct {
			Records []string `json:"records"`
		}
		if err := json.Unmarshal(nonNil(r.Body), &body); err != nil {
			return serverprofile.Operation{}, false, nil
		}
		for _, ref := range body.Records {
			src, _ := refSource(ref)
			out.Resources = append(out.Resources, serverprofile.ResourceScope{Resource: src, Scope: scopeUnknown})
		}
	}
	return out, true, nil
}

// workspaceScopes maps a query's workspaces filter to scope tokens: empty → [scopeAll], else the
// concrete ids.
func workspaceScopes(ws []string) []string {
	if len(ws) == 0 {
		return []string{scopeAll}
	}
	return ws
}

// normalizeSource strips the "@localId" suffix from an EntityRef so the source part can be used
// as a gating key. If there is no "@", the string is already a bare source (or bare localId with
// no app prefix) and is returned as-is.
func normalizeSource(s string) string {
	if src, _ := refSource(s); src != "" {
		return src
	}
	return s
}

func nonNil(b []byte) []byte {
	if b == nil {
		return []byte("{}")
	}
	return b
}
