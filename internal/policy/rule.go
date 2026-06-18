// Package policy is the default-deny rule engine: rules bind a subject (a specific agent or
// "any") + upstream to an outcome (allow/deny/require-approval), with a per-rule rate limit.
// HTTP rules are operation rules — a (method, path-template, query-template) plus a per-variable
// value policy (see internal/optemplate); k8s rules match on namespace/resource/verb.
package policy

import (
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
	OpValuePolicies map[string]ValuePolicy // varName -> per-variable value policy

	// k8s rule dimensions (empty on http rules):
	Namespace string // glob: "", "prod", "prod-*", "*"
	Resource  string // glob over "resource" or "resource/subresource", e.g. "pods", "pods/log", "*"
	Verb      string // "", "*", or one verb: get/list/watch/create/update/patch/delete/deletecollection
}

// ValuePolicy is the per-variable gating policy on an operation rule. Mode "any" auto-allows any
// extracted value (used for date vars and operator-trusted text vars); Mode "set" gates a text
// var against the explicit Values allow-list.
type ValuePolicy struct {
	Type   string   `json:"type"`   // "text" | "date"
	Mode   string   `json:"mode"`   // "set" | "any"
	Values []string `json:"values"` // allowed values (text/set)
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
