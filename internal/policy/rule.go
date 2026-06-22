// Package policy is the default-deny rule engine: rules bind a subject (a specific agent or
// "any") + upstream to an outcome (allow/deny/require-approval), with a per-rule rate limit.
// HTTP rules are operation rules — a (method, path-template, query-template) plus a per-variable
// value policy (see internal/optemplate); k8s rules match on namespace/resource/verb.
package policy

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Outcomes.
const (
	Allow           = "allow"
	Deny            = "deny"
	RequireApproval = "require-approval"
)

// Rule binds a subject+upstream to an outcome. For http upstreams it is an operation rule
// (Op* fields + a per-variable value policy); for k8s clusters it matches on
// Namespace+Resource+Verb.
type Rule struct {
	ID              string
	SubjectAgentID  string // "" = any agent
	UpstreamID      string
	Outcome         string
	RateLimitPerMin int
	CreatedAt       time.Time

	// HTTP operation rule (empty on k8s rules):
	OpMethod        string                 // e.g. "GET"; matched case-insensitively
	OpPathTemplate  string                 // optemplate path with {name:type} placeholders
	OpQueryTemplate map[string]string      // query param -> literal or "{name:type}"
	OpBodyTemplate  map[string]string      // JSON dotted path -> literal or "{name:type}" (request body)
	OpValuePolicies map[string]ValuePolicy // varName -> per-variable value policy

	// k8s rule dimensions (empty on http rules):
	Namespace string // glob: "", "prod", "prod-*", "*"
	Resource  string // glob over "resource" or "resource/subresource", e.g. "pods", "pods/log", "*"
	Verb      string // "", "*", or one verb: get/list/watch/create/update/patch/delete/deletecollection

	// Server-profile rule (empty on raw-http/k8s rules): the profile that owns this rule and its
	// opaque, profile-defined params (see internal/serverprofile).
	Profile       string
	ProfileParams json.RawMessage
}

// ValuePolicy is the per-variable gating policy on an operation rule. Behaviour by Type:
//   - "text": Mode "set" gates against Values; a value NOT in the set → require-approval (the set
//     GROWS on approval). Mode "any" auto-allows.
//   - "date": auto-allowed (the value is type-validated as a date by optemplate.Match).
//   - "number": Mode "range" gates against [Min,Max] inclusive (a nil bound = unbounded that side);
//     a value outside the range → hard DENY. Mode "any" auto-allows any number.
//   - "enum": Mode "set" gates against a CLOSED set Values; a value NOT in the set → hard DENY (an
//     enum is a fixed domain, so the set does NOT auto-grow — unlike text).
type ValuePolicy struct {
	Type   string   `json:"type"`          // "text" | "date" | "number" | "enum"
	Mode   string   `json:"mode"`          // "set" | "any" | "range"
	Values []string `json:"values"`        // allowed values (text/set, enum/set)
	Min    *float64 `json:"min,omitempty"` // number/range lower bound (inclusive); nil = unbounded
	Max    *float64 `json:"max,omitempty"` // number/range upper bound (inclusive); nil = unbounded
}

// ValidOutcome reports whether o is a known outcome.
func ValidOutcome(o string) bool { return o == Allow || o == Deny || o == RequireApproval }

var (
	globMu    sync.Mutex
	globCache = map[string]*regexp.Regexp{}
)

// MatchGlob reports whether path matches pattern, where '*' matches within one path segment
// (no '/') and '**' matches across segments (including '/').
func MatchGlob(pattern, path string) bool {
	globMu.Lock()
	re, ok := globCache[pattern]
	globMu.Unlock()
	if !ok {
		re = compileGlob(pattern)
		globMu.Lock()
		globCache[pattern] = re
		globMu.Unlock()
	}
	return re.MatchString(path)
}

func compileGlob(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch {
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++ // consume the second '*'
		case pattern[i] == '*':
			b.WriteString("[^/]*")
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString("$")
	// regexp.MustCompile is safe: every byte is either a quoted literal or one of our
	// controlled metacharacters.
	return regexp.MustCompile(b.String())
}
