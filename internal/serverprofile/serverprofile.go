// Package serverprofile is the platform-agnostic plugin point for classifying and authorizing
// requests to a specific kind of upstream server. A plugin package registers itself via Register
// in its init(); the core never imports a plugin.
package serverprofile

import (
	"encoding/json"
	"net/url"
	"sync"
)

// Outcomes mirror the policy engine's outcome strings (kept here to avoid importing policy).
const (
	Allow           = "allow"
	Deny            = "deny"
	RequireApproval = "require-approval"
)

// Request is the part of an HTTP request a profile inspects to classify an operation.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Body   []byte // raw request body (may be nil)
}

// ResourceScope is one (resource, scope) pair a request touches. The meaning of the strings is
// profile-defined and opaque to the core (e.g. a plugin may encode a source id + workspace, using
// its own sentinels for "all" / "unknown" scopes).
type ResourceScope struct {
	Resource string
	Scope    string
}

// Operation is a profile's normalized view of a request, used for policy matching and audit.
type Operation struct {
	Kind      string // "read" | "write" | "" (unknown)
	Resources []ResourceScope
	Method    string
	Path      string
}

// Rule is a stored policy rule for a profile: an opaque, profile-defined params blob plus its ID
// and outcome. Outcome is the stored rule's decision string (Allow/Deny/RequireApproval) set by the
// core when it dispatches a rule to a profile's Match method.
type Rule struct {
	ID      string
	Outcome string // "allow" | "deny" | "require-approval" (the stored rule's outcome)
	Params  json.RawMessage
}

// RuleField describes one editable field of a profile rule, so a UI can render an editor.
type RuleField struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // "text" | "enum"
	Options []string `json:"options,omitempty"`
}

// RuleSchema describes a profile's rule shape for the UI.
type RuleSchema struct {
	Profile string      `json:"profile"`
	Fields  []RuleField `json:"fields"`
}

// Profile classifies and authorizes requests for one kind of upstream server.
type Profile interface {
	Name() string
	// Classify reports whether THIS profile handles the request (handled=true). When handled is
	// false the caller uses the generic raw-http engine. A parse error returns handled=false.
	Classify(req Request) (op Operation, handled bool, err error)
	// Match evaluates one of this profile's rules against a handled operation, returning an outcome
	// (Allow/Deny/RequireApproval) and whether the rule matched at all.
	Match(rule Rule, op Operation) (outcome string, matched bool, err error)
	RuleSchema() RuleSchema
}

var (
	mu       sync.RWMutex
	registry = map[string]Profile{}
)

// Register adds a profile under name. Intended to be called from a plugin's init(). A duplicate
// name overwrites (last registration wins) — plugins use unique names.
func Register(name string, p Profile) {
	mu.Lock()
	defer mu.Unlock()
	registry[name] = p
}

// Get returns the registered profile for name.
func Get(name string) (Profile, bool) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// Names returns the registered profile names (unordered).
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	return out
}
