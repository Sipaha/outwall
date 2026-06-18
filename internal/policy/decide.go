package policy

import "strings"

// Input is the request context evaluated against the rules.
type Input struct {
	AgentID, UpstreamID, Method, Path string

	// k8s request tuple (used when Kind=="k8s"):
	Kind        string // "" / "http" = http path matching; "k8s" = (namespace, resource, verb)
	Namespace   string
	Resource    string
	Subresource string
	Verb        string
}

// Decision is the outcome of evaluating a request; Rule is the matched rule (nil on default-deny).
type Decision struct {
	Outcome string
	Rule    *Rule
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

// Decide applies precedence: agent-specific rules outrank any-subject rules outrank
// default-deny; within the chosen tier, most-restrictive wins (deny > require-approval > allow).
func (r *Registry) Decide(in Input) (Decision, error) {
	rules, err := r.ForUpstream(in.UpstreamID)
	if err != nil {
		return Decision{}, err
	}
	k8s := in.Kind == "k8s"
	var agentTier, anyTier []*Rule
	for _, rule := range rules {
		if k8s {
			if !k8sMatches(rule, in) {
				continue
			}
		} else {
			// HTTP operation matching is implemented in Task 3; until then http rules match nothing.
			continue
		}
		switch rule.SubjectAgentID {
		case in.AgentID:
			agentTier = append(agentTier, rule)
		case "":
			anyTier = append(anyTier, rule)
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

// resolveTier picks the most-restrictive outcome among matched rules in one tier.
func resolveTier(tier []*Rule) (Decision, bool) {
	if len(tier) == 0 {
		return Decision{}, false
	}
	var deny, approval, allow *Rule
	for _, r := range tier {
		switch r.Outcome {
		case Deny:
			if deny == nil {
				deny = r
			}
		case RequireApproval:
			if approval == nil {
				approval = r
			}
		case Allow:
			if allow == nil {
				allow = r
			}
		}
	}
	switch {
	case deny != nil:
		return Decision{Outcome: Deny, Rule: deny}, true
	case approval != nil:
		return Decision{Outcome: RequireApproval, Rule: approval}, true
	default:
		return Decision{Outcome: Allow, Rule: allow}, true
	}
}
