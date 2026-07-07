// Package serverprofile is the platform-agnostic plugin point for classifying and authorizing
// requests to a specific kind of upstream server. A plugin package registers itself via Register
// in its init(); the core never imports a plugin.
package serverprofile

import (
	"encoding/json"
	"fmt"
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

// Bindings maps a preset slot key to its chosen value ("*" or a concrete value).
type Bindings map[string]string

// PresetSlot is one typed variable a preset exposes for the agent/operator to fill.
type PresetSlot struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // "text" | "enum"
	Options  []string `json:"options,omitempty"`
	AllowAny bool     `json:"allow_any"` // is "*" a permitted value for this slot?
	Required bool     `json:"required"`
}

// RuleTemplate is a profile-neutral rule a preset expands to. The daemon maps it to a policy.Rule
// (serverprofile must not import policy — policy imports serverprofile). Subject/upstream are set by
// the daemon at fan-out; this carries only the rule's shape.
type RuleTemplate struct {
	Outcome       string          // Allow | Deny | RequireApproval
	BrowseMethods string          // browse rule (coarse method-set), e.g. "GET,HEAD"
	BrowsePath    string          // browse rule path glob, e.g. "/**"
	Profile       string          // profile rule: the profile name (e.g. a registered plugin name)
	ProfileParams json.RawMessage // profile rule params blob
}

// Preset is a named bundle of related rights with typed slots. Build expands it (with bound slot
// values) into rule templates; Build is not serialized (json:"-") — only ID/Label/Slots reach the UI.
type Preset struct {
	ID    string                                 `json:"id"`
	Label string                                 `json:"label"`
	Slots []PresetSlot                           `json:"slots"`
	Build func(Bindings) ([]RuleTemplate, error) `json:"-"`
	// Hint is optional, profile-supplied advice about using this preset on a given upstream (filled
	// by the caller via PresetHint; empty by default). E.g. steer a Citeck upstream off bare browse-get.
	Hint string `json:"hint,omitempty"`
}

// ValidateBindings checks b against slots: every Required slot present and non-empty; "*" allowed
// only when the slot's AllowAny is set; an enum value must be one of Options; unknown keys are an
// error. An empty value for a non-required slot is allowed (skipped).
func ValidateBindings(slots []PresetSlot, b Bindings) error {
	allowed := make(map[string]PresetSlot, len(slots))
	for _, s := range slots {
		allowed[s.Key] = s
	}
	for k := range b {
		if _, ok := allowed[k]; !ok {
			return fmt.Errorf("unknown slot %q", k)
		}
	}
	for _, s := range slots {
		v := b[s.Key]
		if v == "" {
			if s.Required {
				return fmt.Errorf("slot %q is required", s.Key)
			}
			continue
		}
		if v == "*" {
			if !s.AllowAny {
				return fmt.Errorf("slot %q does not allow %q", s.Key, "*")
			}
			continue
		}
		if s.Type == "enum" {
			ok := false
			for _, o := range s.Options {
				if o == v {
					ok = true
					break
				}
			}
			if !ok {
				return fmt.Errorf("slot %q value %q is not an allowed option", s.Key, v)
			}
		}
	}
	return nil
}

// CoreHTTPPresets are the generic presets available on any http upstream regardless of profile.
func CoreHTTPPresets() []Preset {
	return []Preset{{
		ID:    "browse-get",
		Label: "Browse (GET)",
		Build: func(Bindings) ([]RuleTemplate, error) {
			return []RuleTemplate{{Outcome: Allow, BrowseMethods: "GET,HEAD", BrowsePath: "/**"}}, nil
		},
	}}
}

// AvailablePresets is the preset catalog for an upstream: the core http presets (when includeCoreHTTP)
// plus the named profile's presets (if that profile is registered).
func AvailablePresets(includeCoreHTTP bool, profile string) []Preset {
	var out []Preset
	if includeCoreHTTP {
		out = append(out, CoreHTTPPresets()...)
	}
	if p, ok := Get(profile); ok {
		out = append(out, p.Presets()...)
	}
	return out
}

// FindPreset returns the preset with id from the upstream's available catalog.
func FindPreset(includeCoreHTTP bool, profile, id string) (Preset, bool) {
	for _, p := range AvailablePresets(includeCoreHTTP, profile) {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// AuthInput carries everything a profile needs to authorize a classified operation: the operation,
// the raw body (for rewrite), the request mode, and the agent's candidate rules split by tier.
type AuthInput struct {
	Op      Operation
	Body    []byte
	Browser bool
	Agent   []Rule
	Any     []Rule
}

// AuthResult is a profile's full authorization decision for one operation. At most one of
// RewriteBody / Response is non-nil; both nil means "forward the original request".
type AuthResult struct {
	Outcome     string // Allow | Deny | RequireApproval
	RuleID      string // deciding rule id (for audit); "" when synthesized
	RewriteBody []byte // forward this body instead of the original
	Response    []byte // return 200 application/json with this body; do not contact the upstream
}

// Profile classifies and authorizes requests for one kind of upstream server.
type Profile interface {
	Name() string
	// Classify reports whether THIS profile handles the request (handled=true). When handled is
	// false the caller uses the generic raw-http engine. A parse error returns handled=false.
	Classify(req Request) (op Operation, handled bool, err error)
	// Authorize evaluates a handled operation against the agent's candidate rules for this profile
	// (split into Agent / Any tiers) and returns the outcome. A profile may additionally return a
	// rewritten request body or a synthetic response (e.g. to narrow a multi-valued request).
	Authorize(in AuthInput) (AuthResult, error)
	RuleSchema() RuleSchema
	// Presets returns this profile's named rule bundles (may be empty).
	Presets() []Preset
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

// PresetAdvisor is an optional Profile capability: given a preset id an agent may use on this
// profile's upstream, PresetHint returns advisory text steering the agent to a better choice, or
// "" for none. Profiles that don't implement it offer no advice.
type PresetAdvisor interface {
	PresetHint(presetID string) string
}

// PresetHint returns the named profile's advice for a preset id, or "" when the profile is
// unregistered or offers none. This lets the core surface profile-specific guidance — e.g. steer a
// Citeck upstream's read access to citeck-readonly — without embedding any profile logic in core.
func PresetHint(profile, presetID string) string {
	p, ok := Get(profile)
	if !ok {
		return ""
	}
	adv, ok := p.(PresetAdvisor)
	if !ok {
		return ""
	}
	return adv.PresetHint(presetID)
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
