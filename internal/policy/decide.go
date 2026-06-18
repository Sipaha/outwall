package policy

import (
	"net/url"
	"strings"
	"sync"

	"github.com/Sipaha/outwall/internal/optemplate"
)

// Input is the request context evaluated against the rules.
type Input struct {
	AgentID, UpstreamID, Method, Path string

	// HTTP request query (used for operation-template matching when Kind != "k8s").
	Query url.Values

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
	k8s := in.Kind == "k8s"
	var agentTier, anyTier []candidate
	for _, rule := range rules {
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
		if !inSet(vp.Values, val) {
			c.newValues = append(c.newValues, VarValue{Var: v, Value: val})
		}
	}
	if len(c.newValues) > 0 {
		c.outcome = RequireApproval
	}
	return c, true, nil
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
	t, err := optemplate.Parse(rule.OpMethod, rule.OpPathTemplate, rule.OpQueryTemplate)
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
