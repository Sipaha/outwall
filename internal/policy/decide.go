package policy

import (
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/Sipaha/outwall/internal/optemplate"
	"github.com/Sipaha/outwall/internal/serverprofile"
)

// Input is the request context evaluated against the rules.
type Input struct {
	AgentID, UpstreamID, Method, Path string

	// Profile is the upstream's server profile ("raw-http" or a registered plugin name). When set
	// to a registered non-raw-http profile that CLAIMS the request, the profile's rules decide;
	// otherwise evaluation falls through to the raw-http/k8s path (which skips profile rules).
	Profile string

	// HTTP request query (used for operation-template matching when Kind != "k8s").
	Query url.Values

	// Body is the raw HTTP request body (used for body-variable extraction when Kind != "k8s").
	// nil/empty when the request has no body or the proxy did not capture it.
	Body []byte

	// k8s request tuple (used when Kind=="k8s"):
	Kind        string // "" / "http" = http path matching; "k8s" = (namespace, resource, verb)
	Namespace   string
	Resource    string
	Subresource string
	Verb        string
}

// VarValue is a (variable, value) pair extracted from a request.
type VarValue struct {
	Var, Value string
}

// Decision is the outcome of evaluating a request; Rule is the matched rule (nil on default-deny).
type Decision struct {
	Outcome string
	Rule    *Rule

	// HTTP operation results (empty for k8s / default-deny):
	Vars      map[string]string // extracted variable values from the matched template
	NewValues []VarValue        // text (variable,value) pairs not yet in the allowed set
}

// verbMatches: an empty or "*" rule verb matches any verb; otherwise exact (case-insensitive).
func verbMatches(ruleVerb, reqVerb string) bool {
	return ruleVerb == "" || ruleVerb == "*" || strings.EqualFold(ruleVerb, reqVerb)
}

// nsMatches matches a single namespace token against a rule's namespace glob. The safety
// property: an empty request namespace (cluster-scoped / all-namespaces) matches ONLY a
// rule whose namespace is "*" — never a concrete-namespace rule.
func nsMatches(ruleNS, reqNS string) bool {
	if reqNS == "" {
		return ruleNS == "*"
	}
	if ruleNS == "" {
		return false
	}
	return MatchGlob(ruleNS, reqNS)
}

// resourceMatches matches a rule's resource glob against the request resource and, when a
// subresource is present, also against "resource/subresource". Supports "*" and "resource/*".
func resourceMatches(ruleRes, reqResource, reqSub string) bool {
	if ruleRes == "" || ruleRes == "*" {
		return true
	}
	if MatchGlob(ruleRes, reqResource) {
		return true
	}
	if reqSub != "" {
		return MatchGlob(ruleRes, reqResource+"/"+reqSub)
	}
	return false
}

// k8sMatches reports whether a k8s rule matches the request tuple.
func k8sMatches(rule *Rule, in Input) bool {
	return verbMatches(rule.Verb, in.Verb) &&
		nsMatches(rule.Namespace, in.Namespace) &&
		resourceMatches(rule.Resource, in.Resource, in.Subresource)
}

// candidate is a matched rule with its effective outcome for this request. For k8s the
// effective outcome is the rule's static Outcome; for http it is computed by value gating
// (a new text value lifts an allow to require-approval).
type candidate struct {
	rule      *Rule
	outcome   string
	vars      map[string]string
	newValues []VarValue
}

// Decide applies precedence: agent-specific rules outrank any-subject rules outrank
// default-deny; within the chosen tier, most-restrictive wins (deny > require-approval > allow).
func (r *Registry) Decide(in Input) (Decision, error) {
	rules, err := r.ForUpstream(in.UpstreamID)
	if err != nil {
		return Decision{}, err
	}
	// Server-profile path: if the upstream has a registered profile that claims this request, its
	// own rules decide.
	if in.Profile != "" && in.Profile != "raw-http" {
		if prof, ok := serverprofile.Get(in.Profile); ok {
			op, handled, cerr := prof.Classify(serverprofile.Request{Method: in.Method, Path: in.Path, Query: in.Query, Body: in.Body})
			if cerr == nil && handled {
				return decideProfile(prof, in, op, rules)
			}
		} else {
			slog.Warn("unknown server profile; falling back to raw-http", "profile", in.Profile, "upstream", in.UpstreamID)
		}
	}
	k8s := in.Kind == "k8s"
	var agentTier, anyTier []candidate
	for _, rule := range rules {
		if rule.Profile != "" {
			continue // profile rules are evaluated only on the profile path
		}
		var c candidate
		var matched bool
		if k8s {
			if !k8sMatches(rule, in) {
				continue
			}
			c = candidate{rule: rule, outcome: rule.Outcome}
			matched = true
		} else {
			c, matched, err = r.evalHTTPRule(rule, in)
			if err != nil {
				return Decision{}, err
			}
			if !matched {
				continue
			}
		}
		_ = matched
		switch rule.SubjectAgentID {
		case in.AgentID:
			agentTier = append(agentTier, c)
		case "":
			anyTier = append(anyTier, c)
		}
	}
	if d, ok := resolveTier(agentTier); ok {
		return d, nil
	}
	if d, ok := resolveTier(anyTier); ok {
		return d, nil
	}
	return Decision{Outcome: Deny, Rule: nil}, nil // default-deny
}

// decideProfile evaluates the upstream's profile rules (only those whose Profile == in.Profile)
// against the classified operation, applying the same tier precedence as raw-http.
func decideProfile(prof serverprofile.Profile, in Input, op serverprofile.Operation, rules []*Rule) (Decision, error) {
	var agentTier, anyTier []candidate
	vars := opVars(op)
	for _, rule := range rules {
		if rule.Profile != in.Profile {
			continue
		}
		outcome, matched, err := prof.Match(serverprofile.Rule{ID: rule.ID, Outcome: rule.Outcome, Params: rule.ProfileParams}, op)
		if err != nil {
			return Decision{}, err
		}
		if !matched {
			continue
		}
		c := candidate{rule: rule, outcome: outcome, vars: vars}
		switch rule.SubjectAgentID {
		case in.AgentID:
			agentTier = append(agentTier, c)
		case "":
			anyTier = append(anyTier, c)
		}
	}
	if d, ok := resolveTier(agentTier); ok {
		return d, nil
	}
	if d, ok := resolveTier(anyTier); ok {
		return d, nil
	}
	return Decision{Outcome: Deny, Rule: nil}, nil
}

// opVars renders a profile operation as audit variables (op kind + the touched resources/scopes).
func opVars(op serverprofile.Operation) map[string]string {
	if len(op.Resources) == 0 {
		return map[string]string{"op": op.Kind}
	}
	var srcs, scopes []string
	for _, res := range op.Resources {
		srcs = append(srcs, res.Resource)
		scopes = append(scopes, res.Scope)
	}
	return map[string]string{
		"op":        op.Kind,
		"sourceId":  strings.Join(srcs, ","),
		"workspace": strings.Join(scopes, ","),
	}
}

// evalHTTPRule matches the request against one http operation rule's template and, on a
// structural match, gates the extracted text values against the rule's value policies. The
// effective outcome is: the rule's Outcome when every value is allowed; require-approval (with
// the new (var,value) pairs) when an allow/require-approval rule has a not-yet-allowed text
// value. A deny rule stays deny regardless of values.
func (r *Registry) evalHTTPRule(rule *Rule, in Input) (candidate, bool, error) {
	tmpl, err := r.templateFor(rule)
	if err != nil {
		// A malformed stored template never silently grants — it simply does not match.
		return candidate{}, false, nil
	}
	vars, ok := tmpl.Match(in.Method, in.Path, in.Query)
	if !ok {
		return candidate{}, false, nil
	}
	// Body variables are extracted from the real request body and merged into the variable set.
	// A declared body var that is absent / wrong-typed makes the request not match this template.
	if bodyVars, ok := tmpl.ExtractBody(in.Body); ok {
		for k, v := range bodyVars {
			vars[k] = v
		}
	} else {
		return candidate{}, false, nil
	}
	c := candidate{rule: rule, outcome: rule.Outcome, vars: vars}
	if rule.Outcome == Deny {
		return c, true, nil
	}
	for v, val := range vars {
		vp, ok := rule.OpValuePolicies[v]
		if !ok {
			continue // an unconstrained var (no policy) is treated as trusted
		}
		if vp.Type == "date" || vp.Mode == "any" {
			continue // date is type-validated by Match; any auto-allows
		}
		switch vp.Type {
		case "enum":
			// Closed domain: an out-of-set value is a hard deny (the set does NOT grow).
			if !inSet(vp.Values, val) {
				c.outcome = Deny
				return c, true, nil
			}
		case "number":
			// Range gate: an out-of-range value is a hard deny.
			if !inNumberRange(val, vp.Min, vp.Max) {
				c.outcome = Deny
				return c, true, nil
			}
		default: // "text" with mode "set": an unknown value requests approval (the set grows).
			if !inSet(vp.Values, val) {
				c.newValues = append(c.newValues, VarValue{Var: v, Value: val})
			}
		}
	}
	if len(c.newValues) > 0 {
		c.outcome = RequireApproval
	}
	return c, true, nil
}

// inNumberRange reports whether val (parsed as a float) lies within [min,max] inclusive; a nil
// bound is unbounded on that side. A value that does not parse as a number is out of range (it
// should not reach here — optemplate.Match already type-validates number vars — but never grant on
// a parse failure).
func inNumberRange(val string, min, max *float64) bool {
	n, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return false
	}
	if min != nil && n < *min {
		return false
	}
	if max != nil && n > *max {
		return false
	}
	return true
}

func inSet(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

var (
	tmplCacheMu sync.Mutex
	tmplCache   = map[string]optemplate.Template{}
)

// templateFor parses (and caches) the operation template for a rule, keyed by rule ID. The
// cache key includes the rule ID so a rule edited in place (different template) is reparsed only
// when its ID changes — within H1 a template is immutable per rule (only its value-sets grow).
func (r *Registry) templateFor(rule *Rule) (optemplate.Template, error) {
	tmplCacheMu.Lock()
	t, ok := tmplCache[rule.ID]
	tmplCacheMu.Unlock()
	if ok {
		return t, nil
	}
	t, err := optemplate.ParseWithBody(rule.OpMethod, rule.OpPathTemplate, rule.OpQueryTemplate, rule.OpBodyTemplate)
	if err != nil {
		return optemplate.Template{}, err
	}
	tmplCacheMu.Lock()
	tmplCache[rule.ID] = t
	tmplCacheMu.Unlock()
	return t, nil
}

// resolveTier picks the most-restrictive effective outcome among matched candidates in one tier.
func resolveTier(tier []candidate) (Decision, bool) {
	if len(tier) == 0 {
		return Decision{}, false
	}
	var deny, approval, allow *candidate
	for i := range tier {
		switch tier[i].outcome {
		case Deny:
			if deny == nil {
				deny = &tier[i]
			}
		case RequireApproval:
			if approval == nil {
				approval = &tier[i]
			}
		case Allow:
			if allow == nil {
				allow = &tier[i]
			}
		}
	}
	switch {
	case deny != nil:
		return Decision{Outcome: Deny, Rule: deny.rule, Vars: deny.vars}, true
	case approval != nil:
		return Decision{Outcome: RequireApproval, Rule: approval.rule, Vars: approval.vars, NewValues: approval.newValues}, true
	default:
		return Decision{Outcome: Allow, Rule: allow.rule, Vars: allow.vars}, true
	}
}
