package citeck

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/Sipaha/outwall/internal/serverprofile"
)

// emptyRecordsResponse is the synthetic body returned to a browser when a read query is fully
// filtered out — a valid empty Records response so the SPA renders instead of erroring.
var emptyRecordsResponse = []byte(`{"records":[],"hasMore":false,"totalCount":0,"messages":[],"version":1}`)

// filterableRead reports whether op is a workspace-filterable read query: kind "read" with at least
// one resource and no scopeUnknown scope (get-atts reads carry scopeUnknown and are never filtered).
func filterableRead(op serverprofile.Operation) bool {
	if op.Kind != "read" || len(op.Resources) == 0 {
		return false
	}
	for _, res := range op.Resources {
		if res.Scope == scopeUnknown {
			return false
		}
	}
	return true
}

// uniqueSources returns the distinct source strings the operation touches.
func uniqueSources(op serverprofile.Operation) []string {
	seen := map[string]bool{}
	var out []string
	for _, res := range op.Resources {
		if !seen[res.Resource] {
			seen[res.Resource] = true
			out = append(out, res.Resource)
		}
	}
	return out
}

// requestedScopes returns the distinct workspace scopes the operation touches, preserving order.
// For an absent workspaces filter this is exactly [scopeAll].
func requestedScopes(op serverprofile.Operation) []string {
	seen := map[string]bool{}
	var out []string
	for _, res := range op.Resources {
		if !seen[res.Scope] {
			seen[res.Scope] = true
			out = append(out, res.Scope)
		}
	}
	return out
}

// wsAllowedForRead reports whether read access to (source, ws) is granted, applying tier precedence
// (agent tier outranks any tier; within a tier deny outranks allow). require-approval rules do not
// grant here — the filtering path is only reached when the legacy decision was Deny.
func wsAllowedForRead(source, ws string, agent, any []serverprofile.Rule) bool {
	eval := func(rules []serverprofile.Rule) (allowed, matched bool) {
		var hasAllow, hasDeny bool
		for _, r := range rules {
			var p ruleParams
			if err := json.Unmarshal(nonNil(r.Params), &p); err != nil {
				continue
			}
			if p.Op != "" && p.Op != "read" {
				continue
			}
			if !matchSource(p.SourceID, source) || !matchWorkspace(p.Workspace, ws) {
				continue
			}
			switch r.Outcome {
			case serverprofile.Deny:
				hasDeny = true
			case serverprofile.Allow:
				hasAllow = true
			}
		}
		if hasDeny {
			return false, true
		}
		if hasAllow {
			return true, true
		}
		return false, false
	}
	if ok, matched := eval(agent); matched {
		return ok
	}
	if ok, matched := eval(any); matched {
		return ok
	}
	return false
}

// concreteWorkspaces collects the enumerable (non-empty, non-glob) workspace values from the agent's
// read-allow rules across both tiers, for injecting into an absent-workspaces query. Glob/`*` grants
// are not enumerable and are skipped.
func concreteWorkspaces(agent, any []serverprofile.Rule) []string {
	set := map[string]bool{}
	for _, tier := range [][]serverprofile.Rule{agent, any} {
		for _, r := range tier {
			if r.Outcome != serverprofile.Allow {
				continue
			}
			var p ruleParams
			if err := json.Unmarshal(nonNil(r.Params), &p); err != nil {
				continue
			}
			if p.Op != "" && p.Op != "read" {
				continue
			}
			if p.Workspace == "" || strings.Contains(p.Workspace, "*") {
				continue
			}
			set[p.Workspace] = true
		}
	}
	out := make([]string, 0, len(set))
	for w := range set {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

// filterReadQuery is invoked when the legacy decision for a filterable read query was Deny. It applies
// the spec matrix and returns an AuthResult that may rewrite the body or synthesize an empty response.
func filterReadQuery(in serverprofile.AuthInput) (serverprofile.AuthResult, error) {
	sources := uniqueSources(in.Op)
	allowAll := func(ws string) bool {
		for _, s := range sources {
			if !wsAllowedForRead(s, ws, in.Agent, in.Any) {
				return false
			}
		}
		return true
	}

	scopes := requestedScopes(in.Op)
	absent := len(scopes) == 1 && scopes[0] == scopeAll

	var keep []string
	if absent {
		// Legacy was Deny, so there is no wildcard grant covering scopeAll; inject the concrete set.
		for _, w := range concreteWorkspaces(in.Agent, in.Any) {
			if allowAll(w) {
				keep = append(keep, w)
			}
		}
	} else {
		for _, w := range scopes {
			if allowAll(w) {
				keep = append(keep, w)
			}
		}
	}

	// An explicit list whose every workspace is allowed (possibly via separate rules — the legacy
	// resolver only matches when ONE rule covers all touched resources, so this case reaches here)
	// is forwarded unchanged. The absent case never takes this branch: keep is the injected concrete
	// set, which must always be written into the body.
	if !absent && len(keep) == len(scopes) {
		return serverprofile.AuthResult{Outcome: serverprofile.Allow}, nil
	}
	if len(keep) == 0 {
		if in.Browser {
			return serverprofile.AuthResult{Outcome: serverprofile.Allow, Response: emptyRecordsResponse}, nil
		}
		return serverprofile.AuthResult{Outcome: serverprofile.Deny}, nil
	}
	// A partial explicit list is narrowed only for a browser (an API caller that named workspaces
	// must not be silently narrowed). Absent-case injection always rewrites, for both modes.
	if !absent && !in.Browser {
		return serverprofile.AuthResult{Outcome: serverprofile.Deny}, nil
	}
	body, err := rewriteWorkspaces(in.Body, keep)
	if err != nil {
		return serverprofile.AuthResult{Outcome: serverprofile.Deny}, nil
	}
	return serverprofile.AuthResult{Outcome: serverprofile.Allow, RewriteBody: body}, nil
}

// rewriteWorkspaces returns body with query.workspaces replaced by ws, preserving every other field.
func rewriteWorkspaces(body []byte, ws []string) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(nonNil(body), &top); err != nil {
		return nil, err
	}
	var q map[string]json.RawMessage
	if err := json.Unmarshal(nonNil(top["query"]), &q); err != nil {
		return nil, err
	}
	wsJSON, err := json.Marshal(ws)
	if err != nil {
		return nil, err
	}
	q["workspaces"] = wsJSON
	qJSON, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	top["query"] = qJSON
	return json.Marshal(top)
}
